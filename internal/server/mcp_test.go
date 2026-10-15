package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wesleyel/EssayPilot/internal/core"
)

func newTestMCP(t *testing.T) (*core.Service, *mcp.ClientSession) {
	t.Helper()
	s, err := core.NewService(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := newMCPServer(s).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "Test Client", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return s, cs
}

// call 调用工具，把结构化结果解到 out；wantErr 表示期望工具返回 isError。
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any, wantErr bool) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	if res.IsError != wantErr {
		t.Fatalf("%s: isError=%v, want %v: %s", name, res.IsError, wantErr, text)
	}
	if out != nil && !wantErr {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
	}
	return text
}

func dims(c, l, s, v float64) map[string]any {
	d := func(x float64) map[string]any { return map[string]any{"score": x, "feedback": "ok"} }
	return map[string]any{"content": d(c), "language": d(l), "structure": d(s), "convention": d(v)}
}

func TestMCPFlow(t *testing.T) {
	s, cs := newTestMCP(t)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 20 {
		t.Errorf("tools = %d, want 20", len(tools.Tools))
	}

	content := "Nowadays, more and more people realise the importance of health.\nIn my opinion, we should take exercise every day."
	var created essayView
	call(t, cs, "create_essay", map[string]any{
		"subject": "english", "type": "part-b", "title": "Health", "category": "图画·人生态度",
		"tags": []string{"健康"}, "prompt": "Write an essay about health.", "content": content,
	}, &created, false)
	id := created.Essay.ID
	if id == "" || created.Essay.Content != content {
		t.Fatalf("create_essay: %+v", created.Essay)
	}
	if !strings.HasPrefix(created.Numbered, "¶1 Nowadays") || !strings.Contains(created.Numbered, "\n¶2 In my opinion") {
		t.Errorf("numbered = %q", created.Numbered)
	}

	// annotate：一条纠错、一条亮点、一条找不到原文。
	var ann annotateOut
	call(t, cs, "annotate", map[string]any{"essay_id": id, "items": []map[string]any{
		{"quote": "realise", "kind": "correction", "label": "spelling", "note": "美式拼写", "suggestion": "realize"},
		{"quote": "take exercise every day", "kind": "good", "note": "搭配地道"},
		{"quote": "this text does not exist", "kind": "issue", "note": "x"},
	}}, &ann, false)
	if ann.Added != 2 || ann.Failed != 1 || len(ann.Results) != 3 || ann.Results[2].Error == "" {
		t.Fatalf("annotate: %+v", ann)
	}
	corrID := ann.Results[0].Annotation.ID
	if a := ann.Results[0].Annotation; a.Quote != "realise" || a.Author != "agent:test-client" {
		t.Errorf("annotation = %+v", a)
	}

	// submit_evaluation：维度超上限被拒，再提交合法评分。
	eval := map[string]any{
		"essay_id": id, "score": 13.0, "band": "第四档", "dimensions": dims(8, 2, 2, 1),
		"overall_comment": "不错", "strengths": []string{"切题"}, "weaknesses": []string{"偏短"}, "polished": content,
		"corrections": []map[string]any{{"quote": "more and more", "corrected": "an increasing number of", "kind": "polish", "explanation": "更正式"}},
	}
	call(t, cs, "submit_evaluation", eval, nil, true) // content 满分 5，8 超限
	eval["dimensions"] = dims(4, 4, 3, 2)
	var er core.EvaluationResult
	call(t, cs, "submit_evaluation", eval, &er, false)
	if er.Evaluation.Score != 13 || er.Evaluation.MaxScore != 20 || er.Version.ID == "" || len(er.Corrections) != 1 || er.Corrections[0].Annotation == nil {
		t.Fatalf("submit_evaluation: %+v", er)
	}

	// apply_suggestion
	var applied essayView
	call(t, cs, "apply_suggestion", map[string]any{"annotation_id": corrID}, &applied, false)
	if !strings.Contains(applied.Essay.Content, "people realize the importance") {
		t.Errorf("apply_suggestion content = %q", applied.Essay.Content)
	}

	// get_essay 校验正文与标注
	var got essayView
	call(t, cs, "get_essay", map[string]any{"id": id}, &got, false)
	if got.Essay.Content != applied.Essay.Content {
		t.Errorf("get_essay content mismatch")
	}
	byQuote := map[string]core.Annotation{}
	for _, a := range got.Annotations {
		byQuote[a.Quote] = a
	}
	if a := byQuote["realise"]; a.Status != core.StatusResolved {
		t.Errorf("applied annotation status = %q", a.Status)
	}
	good, ok := byQuote["take exercise every day"]
	if !ok || good.Status != core.StatusOpen {
		t.Fatalf("good annotation lost: %+v", got.Annotations)
	}
	if got.Essay.Content[good.Start:good.End] != good.Quote { // 纯 ASCII：UTF-16 偏移即字节偏移
		t.Errorf("good annotation not re-anchored: %d-%d", good.Start, good.End)
	}
	if a, ok := byQuote["more and more"]; !ok || a.Kind != "correction" || a.EvaluationID == nil {
		t.Errorf("evaluation correction annotation = %+v", a)
	}
	if len(got.Evaluations) != 1 {
		t.Errorf("evaluations = %d", len(got.Evaluations))
	}

	// reply：回复用户的疑问
	q, err := s.Annotate("user", id, []core.AnnotateItem{{Quote: "In my opinion", Kind: "question", Note: "这个开头会不会太俗？"}})
	if err != nil || q[0].Annotation == nil {
		t.Fatalf("user annotate: %v %+v", err, q)
	}
	var rep core.Annotation
	call(t, cs, "reply", map[string]any{"annotation_id": q[0].Annotation.ID, "note": "可以换成 From my perspective"}, &rep, false)
	if rep.ParentID == nil || *rep.ParentID != q[0].Annotation.ID || rep.EssayID != id {
		t.Errorf("reply = %+v", rep)
	}
	var mine listAnnotationsOut
	call(t, cs, "list_annotations", map[string]any{"essay_id": id, "author": "user"}, &mine, false)
	if len(mine.Annotations) != 1 {
		t.Errorf("user annotations = %d", len(mine.Annotations))
	}

	// get_rubric / list_types
	var rb rubricOut
	call(t, cs, "get_rubric", map[string]any{"essay_id": id}, &rb, false)
	if rb.Spec.ID != "part-b" || len(rb.Dimensions) != 4 || rb.Dimensions[0].MaxScore != 5 || rb.Text == "" {
		t.Errorf("get_rubric = %+v", rb.Spec)
	}

	// get_ui_context
	s.SetUIContext(core.UIContext{EssayID: id, SelectionStart: 0, SelectionEnd: 8, SelectionText: "Nowadays"})
	var ui uiContextOut
	call(t, cs, "get_ui_context", map[string]any{}, &ui, false)
	if ui.EssayID != id || ui.Title != "Health" || ui.SelectionText != "Nowadays" {
		t.Errorf("get_ui_context = %+v", ui)
	}

	// prompts
	pr, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "grade_essay", Arguments: map[string]string{"essay_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	text := pr.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{id, "submit_evaluation", "get_rubric", "第五档", "Write an essay about health.", "people realize"} {
		if !strings.Contains(text, want) {
			t.Errorf("grade_essay prompt missing %q", want)
		}
	}
	pr, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "review_my_marks", Arguments: map[string]string{"essay_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	if text := pr.Messages[0].Content.(*mcp.TextContent).Text; !strings.Contains(text, "这个开头会不会太俗") || !strings.Contains(text, "reply") {
		t.Errorf("review_my_marks prompt = %s", text)
	}

	// resources
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "essay://" + id})
	if err != nil {
		t.Fatal(err)
	}
	md := rr.Contents[0].Text
	for _, want := range []string{"# Health", "## 正文", "people realize", "take exercise every day", "13 / 20"} {
		if !strings.Contains(md, want) {
			t.Errorf("essay resource missing %q:\n%s", want, md)
		}
	}
	rr, err = cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "rubric://mayuan"})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "马原") {
		t.Errorf("rubric resource: %v", err)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "essay://missing"}); err == nil {
		t.Error("missing essay resource should fail")
	}

	// 活动日志：actor 来自客户端名
	acts := s.Activity(50)
	if len(acts) == 0 || acts[0].Actor != "agent:test-client" {
		t.Fatalf("activity = %+v", acts)
	}
	found := false
	for _, a := range acts {
		if a.Tool == "annotate" && a.Summary == "新增 2 条标注，1 条未找到原文" && a.EssayID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("annotate activity not logged: %+v", acts)
	}

	// delete_essay
	call(t, cs, "delete_essay", map[string]any{"id": id}, nil, false)
	call(t, cs, "get_essay", map[string]any{"id": id}, nil, true)
}

func TestPromptText(t *testing.T) {
	d := core.EssayDetail{Essay: core.Essay{ID: "e1", Subject: core.Politics, Type: "mayuan", Title: "矛盾", Prompt: "结合材料说明", Content: "①矛盾是事物发展的动力。"}}
	for _, name := range core.PromptNames {
		text, err := core.PromptText(name, d)
		if err != nil || !strings.Contains(text, "e1") || !strings.Contains(text, "①矛盾是事物发展的动力。") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := core.PromptText("nope", d); err == nil {
		t.Error("unknown prompt should fail")
	}
}

func TestMCPHTTP(t *testing.T) {
	s, err := core.NewService(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateEssay("user", core.CreateEssayInput{Subject: core.Politics, Type: "mayuan", Tags: []string{"矛盾"}, Content: "答"}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newMCPHandler(s))
	defer ts.Close()

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var out listEssaysOut
	call(t, cs, "list_essays", map[string]any{"subject": "politics"}, &out, false)
	if len(out.Essays) != 1 || out.Essays[0].Title != "未命名" {
		t.Errorf("list_essays = %+v", out.Essays)
	}
	acts := s.Activity(1)
	if len(acts) != 1 || !strings.HasPrefix(acts[0].Actor, "agent:") {
		t.Errorf("activity = %+v", acts)
	}
	t.Logf("HTTP actor: %s", acts[0].Actor)
}
