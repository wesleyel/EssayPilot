package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type apiClient struct {
	t   *testing.T
	srv *httptest.Server
}

func newAPIClient(t *testing.T) (*apiClient, *Service) {
	s := newTestService(t)
	srv := httptest.NewServer(newAPI(s, APIOptions{MCPEnabled: true, MCPPath: "/mcp", WebMCP: true}))
	t.Cleanup(srv.Close)
	return &apiClient{t, srv}, s
}

// do 发请求，断言状态码，并把响应解码到 out（可为 nil）。
func (c *apiClient) do(method, path, actor string, body any, status int, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if actor != "" {
		req.Header.Set("X-Actor", actor)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, status, raw)
	}
	if status >= 400 {
		var e struct{ Error string }
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			c.t.Fatalf("%s %s: bad error body %s", method, path, raw)
		}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode: %v: %s", method, path, err, raw)
		}
	}
}

func TestAPIEndToEnd(t *testing.T) {
	c, s := newAPIClient(t)

	var meta map[string]any
	c.do("GET", "/api/meta", "", nil, 200, &meta)
	if meta["webmcp"] != true || meta["mcp"].(map[string]any)["path"] != "/mcp" || len(meta["types"].([]any)) != len(Types) {
		t.Fatalf("meta: %v", meta)
	}

	var d EssayDetail
	c.do("POST", "/api/essays", "", CreateEssayInput{Subject: English, Type: "part-a", Category: "建议信", Tags: []string{"信"}, Content: "Dear Tom, I has a idea."}, 201, &d)
	id := d.Essay.ID
	c.do("POST", "/api/essays", "", CreateEssayInput{Subject: English, Type: "part-a", Tags: []string{"信"}}, 400, nil)
	c.do("POST", "/api/essays", "", "not json", 400, nil)

	var list []EssaySummary
	c.do("GET", "/api/essays?subject=english&tag=%E4%BF%A1", "", nil, 200, &list)
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %+v", list)
	}
	c.do("GET", "/api/essays/nope", "", nil, 404, nil)
	c.do("PATCH", "/api/essays/"+id, "", UpdateEssayInput{Title: ptrTo("给 Tom 的信")}, 200, &d)
	if d.Essay.Title != "给 Tom 的信" {
		t.Fatalf("patch: %+v", d.Essay)
	}

	var results []AnnotateResult
	c.do("POST", "/api/essays/"+id+"/annotations", "agent:webmcp", map[string]any{"items": []AnnotateItem{
		{Quote: "has", Kind: "correction", Suggestion: ptrTo("have")},
		{Quote: "a idea", Kind: "correction", Suggestion: ptrTo("an idea")},
		{Quote: "zzz", Kind: "issue"},
	}}, 200, &results)
	if results[0].Annotation == nil || results[0].Annotation.Author != "agent:webmcp" || results[2].Error == "" {
		t.Fatalf("annotate: %+v", results)
	}
	if acts := s.Activity(10); len(acts) == 0 || acts[0].Actor != "agent:webmcp" || acts[0].EssayID != id {
		t.Fatalf("activity: %+v", acts)
	}
	var acts []Activity
	c.do("GET", "/api/activity?limit=1", "", nil, 200, &acts)
	if len(acts) != 1 {
		t.Fatalf("activity api: %+v", acts)
	}

	aid := results[0].Annotation.ID
	var ann Annotation
	c.do("PATCH", "/api/annotations/"+aid, "", AnnotationPatch{Note: ptrTo("主谓一致")}, 200, &ann)
	if ann.Note != "主谓一致" {
		t.Fatalf("patch annotation: %+v", ann)
	}
	c.do("PATCH", "/api/annotations/"+aid, "", AnnotationPatch{Kind: ptrTo("bogus")}, 400, nil)
	c.do("POST", "/api/annotations/"+aid+"/apply", "", nil, 200, &d)
	if d.Essay.Content != "Dear Tom, I have a idea." {
		t.Fatalf("apply one: %q", d.Essay.Content)
	}
	c.do("POST", "/api/essays/"+id+"/apply", "", nil, 200, &d)
	if d.Essay.Content != "Dear Tom, I have an idea." {
		t.Fatalf("apply all: %q", d.Essay.Content)
	}
	c.do("POST", "/api/essays/"+id+"/apply", "", map[string]any{"ids": []string{}}, 400, nil)

	// 评分：Part A 满分 10，每维 2.5
	bad := EvaluationInput{Score: 9, Dimensions: map[string]DimensionInput{"content": {Score: 2}, "language": {Score: 2}, "structure": {Score: 2}, "convention": {Score: 2}}}
	c.do("POST", "/api/essays/"+id+"/evaluations", "agent:x", bad, 400, nil)
	good := bad
	good.Score = 8
	var er EvaluationResult
	c.do("POST", "/api/essays/"+id+"/evaluations", "agent:x", good, 201, &er)
	if er.Evaluation.Score != 8 || er.Version.Note != "评分快照" {
		t.Fatalf("evaluation: %+v", er)
	}

	var v Version
	c.do("POST", "/api/essays/"+id+"/versions", "", map[string]string{"note": "初稿"}, 201, &v)
	c.do("POST", "/api/essays/"+id+"/versions", "", nil, 201, nil)
	c.do("PATCH", "/api/essays/"+id, "", UpdateEssayInput{Content: ptrTo("changed")}, 200, nil)
	c.do("POST", "/api/essays/"+id+"/versions/"+v.ID+"/restore", "", nil, 200, &d)
	if d.Essay.Content != "Dear Tom, I have an idea." {
		t.Fatalf("restore: %q", d.Essay.Content)
	}
	c.do("DELETE", "/api/essays/"+id+"/evaluations/"+er.Evaluation.ID, "", nil, 204, nil)
	c.do("DELETE", "/api/essays/"+id+"/evaluations/"+er.Evaluation.ID, "", nil, 404, nil)
	c.do("DELETE", "/api/essays/"+id+"/versions/"+v.ID, "", nil, 204, nil)
	c.do("DELETE", "/api/annotations/"+aid, "", nil, 204, nil)
	c.do("DELETE", "/api/annotations/"+aid, "", nil, 404, nil)

	var rb struct {
		Spec TypeSpec `json:"spec"`
		Text string   `json:"text"`
	}
	c.do("GET", "/api/rubric/part-a", "", nil, 200, &rb)
	if rb.Spec.ID != "part-a" || rb.Text == "" {
		t.Fatalf("rubric: %+v", rb)
	}
	c.do("GET", "/api/rubric/nope", "", nil, 404, nil)
	var pt struct{ Text string }
	c.do("GET", "/api/prompts/grade_essay?essay="+id, "", nil, 200, &pt)
	if pt.Text == "" {
		t.Fatal("empty prompt")
	}
	c.do("GET", "/api/prompts/grade_essay", "", nil, 400, nil)
	c.do("GET", "/api/prompts/grade_essay?essay=nope", "", nil, 404, nil)

	var ui UIContext
	c.do("PUT", "/api/ui-context", "", UIContext{EssayID: id, SelectionStart: 1, SelectionEnd: 2}, 200, &ui)
	c.do("GET", "/api/ui-context", "", nil, 200, &ui)
	if ui.EssayID != id || ui.UpdatedAt == "" {
		t.Fatalf("ui-context: %+v", ui)
	}

	c.do("POST", "/api/essays", "robot", CreateEssayInput{}, 400, nil)
	c.do("POST", "/api/essays", "agent:", CreateEssayInput{}, 400, nil)
	c.do("GET", "/api/nope", "", nil, 404, nil)
	c.do("DELETE", "/api/essays/"+id, "", nil, 204, nil)
	c.do("DELETE", "/api/essays/"+id, "", nil, 404, nil)
}

func TestAPIEvents(t *testing.T) {
	c, s := newAPIClient(t)
	d := newEssay(t, s, "hello world")

	res, err := http.Get(c.srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitFor := func(prefix string) string {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed waiting for %q", prefix)
				}
				if strings.HasPrefix(l, prefix) {
					return l
				}
			case <-timeout:
				t.Fatalf("timeout waiting for %q", prefix)
			}
		}
	}
	waitFor(": connected")

	c.do("POST", "/api/essays/"+d.Essay.ID+"/annotations", "agent:bot", map[string]any{"items": []AnnotateItem{{Quote: "world", Kind: "good"}}}, 200, nil)
	waitFor("event: annotations")
	var ev Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(waitFor("data: "), "data: ")), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "annotations" || ev.EssayID != d.Essay.ID || ev.Actor != "agent:bot" {
		t.Fatalf("event: %+v", ev)
	}
	waitFor("event: activity")

	// 断开后退订
	res.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.hub.mu.Lock()
		n := len(s.hub.subs)
		s.hub.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %d subscribers after disconnect", n)
		}
		s.Log("agent:x", "poke", "", "") // 触发写入，让服务端察觉断开
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAPIPing(t *testing.T) {
	old := ssePing
	ssePing = 20 * time.Millisecond
	defer func() { ssePing = old }()
	s := newTestService(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	newAPI(s, APIOptions{}).ServeHTTP(rec, req.WithContext(ctx))
	if !strings.Contains(rec.Body.String(), ": ping\n\n") {
		t.Fatalf("no ping in %q", rec.Body.String())
	}
}
