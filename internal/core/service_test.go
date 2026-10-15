package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ptrTo[T any](v T) *T { return &v }

func validEval() EvaluationInput {
	return EvaluationInput{
		Score: 15.5, Band: "第四档",
		Dimensions: map[string]DimensionInput{
			"content": {Score: 4}, "language": {Score: 4}, "structure": {Score: 4}, "convention": {Score: 3.5},
		},
	}
}

func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func TestCreateUpdateValidation(t *testing.T) {
	s := newTestService(t)
	bad := []CreateEssayInput{
		{Subject: "math", Type: "part-a", Tags: []string{"x"}},
		{Subject: English, Type: "mayuan", Tags: []string{"x"}},
		{Subject: English, Type: "part-a", Category: "不存在", Tags: []string{"x"}},
		{Subject: English, Type: "part-a", Category: "建议信", Tags: []string{" ", ""}},
		{Subject: Politics, Type: "mayuan", Category: "建议信", Tags: []string{"x"}},
	}
	for i, in := range bad {
		if _, err := s.CreateEssay("user", in); !isValidation(err) {
			t.Errorf("case %d: want validation error, got %v", i, err)
		}
	}
	d, err := s.CreateEssay("user", CreateEssayInput{Subject: Politics, Type: "mayuan", Tags: []string{" 矛盾 ", "矛盾"}, Content: "矛盾是事物发展的动力 and more"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Essay.Title != "未命名" || len(d.Essay.Tags) != 1 || d.Essay.WordCount != 12 {
		t.Fatalf("bad essay: %+v", d.Essay)
	}
	if _, err := s.UpdateEssay("user", d.Essay.ID, UpdateEssayInput{Type: ptrTo("part-a")}); !isValidation(err) {
		t.Fatalf("cross-subject type change: %v", err)
	}
	if _, err := s.UpdateEssay("user", d.Essay.ID, UpdateEssayInput{Tags: &[]string{}}); !isValidation(err) {
		t.Fatalf("empty tags: %v", err)
	}
	d, err = s.UpdateEssay("user", d.Essay.ID, UpdateEssayInput{Type: ptrTo("shigang"), Title: ptrTo(" 史纲 ")})
	if err != nil || d.Essay.Type != "shigang" || d.Essay.Title != "史纲" {
		t.Fatalf("update: %v %+v", err, d.Essay)
	}
	if _, err := s.GetEssay("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListEssays(t *testing.T) {
	s := newTestService(t)
	a := newEssay(t, s, "apple pie")
	time.Sleep(time.Millisecond)
	b, _ := s.CreateEssay("user", CreateEssayInput{Subject: Politics, Type: "defa", Tags: []string{"法治"}, Prompt: "论法治"})
	list, err := s.ListEssays(EssayFilter{})
	if err != nil || len(list) != 2 || list[0].ID != b.Essay.ID {
		t.Fatalf("list order: %v %+v", err, list)
	}
	if l, _ := s.ListEssays(EssayFilter{Tag: "法治"}); len(l) != 1 || l[0].ID != b.Essay.ID {
		t.Fatalf("tag filter: %+v", l)
	}
	if l, _ := s.ListEssays(EssayFilter{Query: "APPLE"}); len(l) != 1 || l[0].ID != a.Essay.ID {
		t.Fatalf("query filter: %+v", l)
	}
	if l, _ := s.ListEssays(EssayFilter{Subject: "english", Type: "part-b"}); len(l) != 1 {
		t.Fatalf("subject filter: %+v", l)
	}
	if _, err := s.SubmitEvaluation("agent:x", a.Essay.ID, validEval()); err != nil {
		t.Fatal(err)
	}
	annotate(t, s, a.Essay.ID, AnnotateItem{Quote: "pie", Kind: "good"})
	list, _ = s.ListEssays(EssayFilter{})
	if list[0].ID != a.Essay.ID || list[0].LatestScore == nil || list[0].LatestScore.Score != 15.5 ||
		list[0].AnnotationCount != 1 || list[0].VersionCount != 1 {
		t.Fatalf("summary: %+v", list[0])
	}
}

func TestAnnotateValidation(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "hello world")
	res, err := s.Annotate("user", d.Essay.ID, []AnnotateItem{
		{Quote: "hello", Kind: "bogus"},
		{Quote: "world", Kind: "correction"},
		{Quote: "missing", Kind: "issue"},
		{Quote: "world", Kind: "correction", Suggestion: ptrTo("World"), Label: "拼写"},
		{Kind: "highlight", Start: ptrTo(0), End: ptrTo(5), Quote: "nope"},
		{Kind: "highlight", Start: ptrTo(0), End: ptrTo(5)},
		{Kind: "issue", ParentID: ptrTo("nope")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, wantOK := range []bool{false, false, false, true, false, true, false} {
		if (res[i].Error == "") != wantOK || res[i].Index != i {
			t.Errorf("item %d: %+v", i, res[i])
		}
	}
	if _, err := s.Annotate("user", "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing essay: %v", err)
	}
	l, _ := s.ListAnnotations(d.Essay.ID, AnnotationFilter{Author: "user", Kind: "correction"})
	if len(l) != 1 || l[0].Label != "拼写" {
		t.Fatalf("filter: %+v", l)
	}
	if l, _ := s.ListAnnotations(d.Essay.ID, AnnotationFilter{Author: "agent"}); len(l) != 0 {
		t.Fatalf("agent filter: %+v", l)
	}
}

func TestUpdateDeleteAnnotation(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "one two three")
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "two", Kind: "issue"})[0]
	r := annotate(t, s, d.Essay.ID, AnnotateItem{ParentID: &a.ID, Kind: "question", Note: "why"})[0]
	got, err := s.UpdateAnnotation("user", a.ID, AnnotationPatch{Start: ptrTo(8), End: ptrTo(13), Note: ptrTo("n")})
	if err != nil || got.Quote != "three" || got.Note != "n" {
		t.Fatalf("relocate: %v %+v", err, got)
	}
	d, _ = s.GetEssay(d.Essay.ID)
	if rr := find(t, d, r.ID); rr.Quote != "three" {
		t.Fatalf("reply did not follow: %+v", rr)
	}
	if _, err := s.UpdateAnnotation("user", a.ID, AnnotationPatch{Status: ptrTo("orphaned")}); !isValidation(err) {
		t.Fatalf("status orphaned: %v", err)
	}
	if _, err := s.UpdateAnnotation("user", r.ID, AnnotationPatch{Start: ptrTo(0), End: ptrTo(1)}); !isValidation(err) {
		t.Fatalf("reply relocate: %v", err)
	}
	if err := s.DeleteAnnotation("user", a.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetEssay(d.Essay.ID)
	if len(d.Annotations) != 0 {
		t.Fatalf("reply should be deleted with parent: %+v", d.Annotations)
	}
	if err := s.DeleteAnnotation("user", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}

func TestApplySuggestion(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "I has a apple. He go to school. It is good.")
	anns := annotate(t, s, d.Essay.ID,
		AnnotateItem{Quote: "has", Kind: "correction", Suggestion: ptrTo("have")},
		AnnotateItem{Quote: "a apple", Kind: "correction", Suggestion: ptrTo("an apple")},
		AnnotateItem{Quote: "go", Kind: "correction", Suggestion: ptrTo("goes")},
		AnnotateItem{Quote: "school", Kind: "highlight"},
		AnnotateItem{Quote: "It is good", Kind: "good"},
	)
	reply := annotate(t, s, d.Essay.ID, AnnotateItem{ParentID: &anns[0].ID, Note: "ok"})[0]

	d, err := s.ApplySuggestion("user", anns[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Essay.Content != "I have a apple. He go to school. It is good." {
		t.Fatalf("content: %q", d.Essay.Content)
	}
	applied := find(t, d, anns[0].ID)
	if applied.Status != StatusResolved || applied.Start != 2 || applied.End != 6 {
		t.Fatalf("applied: %+v", applied)
	}
	if find(t, d, reply.ID).Status != StatusResolved {
		t.Fatal("reply should follow resolved parent")
	}
	if got := find(t, d, anns[3].ID); got.Start != 25 {
		t.Fatalf("highlight shifted: %+v", got)
	}
	assertAnchored(t, d)
	if _, err := s.ApplySuggestion("user", anns[0].ID); !isValidation(err) {
		t.Fatalf("re-apply resolved: %v", err)
	}
	if _, err := s.ApplySuggestion("user", anns[3].ID); !isValidation(err) {
		t.Fatalf("apply without suggestion: %v", err)
	}

	// 其余全部：从后往前替换
	d, err = s.ApplySuggestions("user", d.Essay.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Essay.Content != "I have an apple. He goes to school. It is good." {
		t.Fatalf("content: %q", d.Essay.Content)
	}
	for _, i := range []int{1, 2} {
		if find(t, d, anns[i].ID).Status != StatusResolved {
			t.Fatalf("ann %d not resolved", i)
		}
	}
	if got := find(t, d, anns[4].ID); got.Quote != "It is good" || got.Start != 36 {
		t.Fatalf("good shifted: %+v", got)
	}
	assertAnchored(t, d)
	if _, err := s.ApplySuggestions("user", d.Essay.ID, nil); !isValidation(err) {
		t.Fatalf("nothing left: %v", err)
	}
}

func TestApplySuggestionsOverlapAndIDs(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "very very good 😀 day")
	anns := annotate(t, s, d.Essay.ID,
		AnnotateItem{Quote: "very very", Kind: "correction", Suggestion: ptrTo("very")},
		AnnotateItem{Quote: "very good", Kind: "correction", Suggestion: ptrTo("great")},
		AnnotateItem{Quote: "day", Kind: "correction", Suggestion: ptrTo("")},
	)
	d, err := s.ApplySuggestions("user", d.Essay.ID, []string{anns[2].ID, anns[1].ID, anns[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if d.Essay.Content != "very good 😀 " {
		t.Fatalf("content: %q", d.Essay.Content)
	}
	if got := find(t, d, anns[1].ID); got.Status == StatusResolved {
		t.Fatalf("overlapping one should not be applied: %+v", got)
	}
	if _, err := s.ApplySuggestions("user", d.Essay.ID, []string{"nope"}); !isValidation(err) {
		t.Fatalf("foreign id: %v", err)
	}
}

func TestSubmitEvaluationValidation(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "I has a apple.")
	mut := []func(*EvaluationInput){
		func(in *EvaluationInput) { delete(in.Dimensions, "convention") },
		func(in *EvaluationInput) { in.Dimensions["extra"] = DimensionInput{} },
		func(in *EvaluationInput) {
			delete(in.Dimensions, "convention")
			in.Dimensions["extra"] = DimensionInput{Score: 3.5}
		},
		func(in *EvaluationInput) { in.Dimensions["content"] = DimensionInput{Score: 5.5}; in.Score = 17 },
		func(in *EvaluationInput) { in.Dimensions["content"] = DimensionInput{Score: -1}; in.Score = 10.5 },
		func(in *EvaluationInput) { in.Score = 15.6 },
		func(in *EvaluationInput) { in.Score = 21 },
		func(in *EvaluationInput) {
			in.Corrections = []CorrectionInput{{Quote: "has", Corrected: "have", Kind: "concept"}}
		},
	}
	for i, m := range mut {
		in := validEval()
		m(&in)
		if _, err := s.SubmitEvaluation("agent:x", d.Essay.ID, in); !isValidation(err) {
			t.Errorf("case %d: want validation error, got %v", i, err)
		}
	}
	in := validEval()
	in.Score = 15.54 // 允许 0.05 误差
	if _, err := s.SubmitEvaluation("agent:x", d.Essay.ID, in); err != nil {
		t.Fatalf("tolerance: %v", err)
	}
	empty := newEssay(t, s, "  ")
	if _, err := s.SubmitEvaluation("agent:x", empty.Essay.ID, validEval()); !isValidation(err) {
		t.Fatalf("empty content: %v", err)
	}
	if _, err := s.SubmitEvaluation("agent:x", "nope", validEval()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing essay: %v", err)
	}
}

func TestSubmitEvaluationCorrections(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "I has a apple. I has a pen.")
	ch, cancel := s.Subscribe()
	defer cancel()
	in := validEval()
	in.Strengths = nil
	in.Corrections = []CorrectionInput{
		{Quote: "has", Occurrence: 2, Corrected: "have", Kind: "grammar", Explanation: "主谓一致"},
		{Quote: "a apple", Corrected: "an apple", Kind: "spelling"},
		{Quote: "missing", Corrected: "x", Kind: "polish"},
	}
	res, err := s.SubmitEvaluation("agent:claude", d.Essay.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ev := res.Evaluation
	if ev.MaxScore != 20 || ev.Dimensions["content"].MaxScore != 5 || ev.Strengths == nil || ev.Author != "agent:claude" {
		t.Fatalf("evaluation: %+v", ev)
	}
	if res.Version.Note != "评分快照" || *res.Version.EvaluationID != ev.ID || *res.Version.Score != 15.5 || ev.VersionID != res.Version.ID {
		t.Fatalf("version: %+v", res.Version)
	}
	c := res.Corrections
	if c[0].Annotation == nil || c[0].Annotation.Start != 17 || c[0].Annotation.Label != "语法语病" ||
		c[0].Annotation.Note != "主谓一致" || *c[0].Annotation.Suggestion != "have" || *c[0].Annotation.EvaluationID != ev.ID {
		t.Fatalf("correction 0: %+v", c[0])
	}
	if c[1].Annotation.Label != "错别字" || c[2].Error == "" || c[2].Index != 2 {
		t.Fatalf("corrections: %+v", c)
	}
	seen := map[string]bool{}
	for len(ch) > 0 {
		e := <-ch
		seen[e.Type] = e.Actor == "agent:claude"
	}
	if !seen["versions"] || !seen["evaluations"] || !seen["annotations"] {
		t.Fatalf("events: %v", seen)
	}

	// 删除评分快照版本：评分随之删除，标注保留但 evaluationId 置空
	if err := s.DeleteVersion("user", d.Essay.ID, res.Version.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetEssay(d.Essay.ID)
	if len(got.Evaluations) != 0 || len(got.Versions) != 0 || len(got.Annotations) != 2 || got.Annotations[0].EvaluationID != nil {
		t.Fatalf("after delete version: %+v", got)
	}

	res, _ = s.SubmitEvaluation("agent:claude", d.Essay.ID, validEval())
	if err := s.DeleteEvaluation("user", d.Essay.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing evaluation: %v", err)
	}
	if err := s.DeleteEvaluation("user", d.Essay.ID, res.Evaluation.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetEssay(d.Essay.ID)
	if len(got.Evaluations) != 0 || len(got.Versions) != 1 || got.Versions[0].EvaluationID != nil || got.Versions[0].Score != nil {
		t.Fatalf("after delete evaluation: %+v", got)
	}
}

func TestRestoreVersion(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "Old text with a key phrase here.")
	v1, err := s.SaveVersion("user", d.Essay.ID, "")
	if err != nil || v1.Note != "手动保存" {
		t.Fatalf("save: %v %+v", err, v1)
	}
	d = setContent(t, s, d.Essay.ID, "New text. Then a key phrase.")
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "key phrase", Kind: "highlight"})[0]
	d, err = s.UpdateEssay("user", d.Essay.ID, UpdateEssayInput{Title: ptrTo("改过的标题")})
	if err != nil {
		t.Fatal(err)
	}

	d, err = s.RestoreVersion("user", d.Essay.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Essay.Content != "Old text with a key phrase here." || d.Essay.Title != "未命名" {
		t.Fatalf("restored: %+v", d.Essay)
	}
	if len(d.Versions) != 2 || d.Versions[0].Note != "恢复前自动保存" || d.Versions[0].Content != "New text. Then a key phrase." {
		t.Fatalf("versions: %+v", d.Versions)
	}
	if got := find(t, d, a.ID); got.Start != 16 || got.Status != StatusOpen {
		t.Fatalf("annotation after restore: %+v", got)
	}
	assertAnchored(t, d)
	if _, err := s.RestoreVersion("user", d.Essay.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	other := newEssay(t, s, "x")
	if _, err := s.RestoreVersion("user", other.Essay.ID, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign version: %v", err)
	}
}

func TestDeleteEssayCascade(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "abc def")
	annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "abc", Kind: "good"})
	if _, err := s.SubmitEvaluation("agent:x", d.Essay.ID, validEval()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEssay("user", d.Essay.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT (SELECT count(*) FROM annotations) + (SELECT count(*) FROM versions) + (SELECT count(*) FROM evaluations)`).Scan(&n)
	if n != 0 {
		t.Fatalf("cascade left %d rows", n)
	}
	if err := s.DeleteEssay("user", d.Essay.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}

func TestActivityAndUIContext(t *testing.T) {
	s := newTestService(t)
	ch, cancel := s.Subscribe()
	for i := range activityCap + 5 {
		s.Log("agent:x", "tool", "", strings.Repeat("a", i%3))
	}
	all := s.Activity(0)
	if len(all) != activityCap || all[0].ID != activityCap+5 || all[len(all)-1].ID != 6 {
		t.Fatalf("ring: len=%d first=%d last=%d", len(all), all[0].ID, all[len(all)-1].ID)
	}
	if got := s.Activity(3); len(got) != 3 || got[0].ID != activityCap+5 {
		t.Fatalf("limit: %+v", got)
	}
	// 订阅者缓冲满了不阻塞
	if len(ch) != subscriberBuf {
		t.Fatalf("buffer: %d", len(ch))
	}
	cancel()
	cancel()

	s.SetUIContext(UIContext{EssayID: "e1", SelectionStart: 1, SelectionEnd: 3, SelectionText: "ab"})
	if c := s.UIContext(); c.EssayID != "e1" || c.UpdatedAt == "" {
		t.Fatalf("ui context: %+v", c)
	}
}
