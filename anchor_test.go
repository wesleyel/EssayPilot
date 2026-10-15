package main

import (
	"strings"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newEssay(t *testing.T, s *Service, content string) EssayDetail {
	t.Helper()
	d, err := s.CreateEssay("user", CreateEssayInput{
		Subject: English, Type: "part-b", Category: "图画·人生态度", Tags: []string{"测试"}, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func annotate(t *testing.T, s *Service, essayID string, items ...AnnotateItem) []Annotation {
	t.Helper()
	res, err := s.Annotate("agent:test", essayID, items)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]Annotation, len(res))
	for i, r := range res {
		if r.Error != "" {
			t.Fatalf("item %d: %s", i, r.Error)
		}
		out[i] = *r.Annotation
	}
	return out
}

func setContent(t *testing.T, s *Service, id, content string) EssayDetail {
	t.Helper()
	d, err := s.UpdateEssay("user", id, UpdateEssayInput{Content: &content})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func find(t *testing.T, d EssayDetail, id string) Annotation {
	t.Helper()
	for _, a := range d.Annotations {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("annotation %s not found", id)
	return Annotation{}
}

// assertAnchored：所有 open 标注的区间文本都等于 quote。
func assertAnchored(t *testing.T, d EssayDetail) {
	t.Helper()
	text := u16(d.Essay.Content)
	for _, a := range d.Annotations {
		if a.Status != StatusOpen {
			continue
		}
		if a.End > len(text) || s16(text[a.Start:a.End]) != a.Quote {
			t.Errorf("annotation %s [%d,%d) quote %q not anchored in %q", a.ID, a.Start, a.End, a.Quote, d.Essay.Content)
		}
	}
}

func TestAnchorInsertDelete(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "The cat sat on the mat.")
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "cat", Kind: "highlight"})[0]
	if a.Start != 4 || a.End != 7 || a.Prefix != "The " || a.Suffix != " sat on the mat." {
		t.Fatalf("bad anchor %+v", a)
	}

	d = setContent(t, s, d.Essay.ID, "Look! The cat sat on the mat.")
	if got := find(t, d, a.ID); got.Start != 10 || got.End != 13 || got.Status != StatusOpen {
		t.Fatalf("after insert: %+v", got)
	}
	d = setContent(t, s, d.Essay.ID, "cat sat on the mat.")
	if got := find(t, d, a.ID); got.Start != 0 || got.End != 3 || got.Prefix != "" {
		t.Fatalf("after delete: %+v", got)
	}
	// 在标注之后改动不影响偏移
	d = setContent(t, s, d.Essay.ID, "cat sat on the rug!")
	if got := find(t, d, a.ID); got.Start != 0 || got.Suffix != " sat on the rug!" {
		t.Fatalf("after tail edit: %+v", got)
	}
	assertAnchored(t, d)
}

func TestAnchorOccurrence(t *testing.T) {
	s := newTestService(t)
	text := "the cat and the dog and the bird"
	d := newEssay(t, s, text)
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "the", Occurrence: 2, Kind: "issue", Note: "x"})[0]
	if a.Start != 12 || a.End != 15 {
		t.Fatalf("occurrence 2: %+v", a)
	}
	res, _ := s.Annotate("user", d.Essay.ID, []AnnotateItem{{Quote: "the", Occurrence: 4, Kind: "issue"}})
	if res[0].Error == "" {
		t.Fatal("occurrence 4 should fail")
	}

	// 在第一处 the 之前插入新的 the：仍跟随原来的第二处
	d = setContent(t, s, d.Essay.ID, "the the cat and the dog and the bird")
	if got := find(t, d, a.ID); got.Start != 16 {
		t.Fatalf("after insert: %+v", got)
	}
}

func TestAnchorContextSearch(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "foo good bar. bazz good quxx.")
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "good", Occurrence: 2, Kind: "good"})[0]
	// 句子对调：区间整体被改动，靠上下文找回原来那一处
	d = setContent(t, s, d.Essay.ID, "bazz good quxx. foo good bar.")
	if got := find(t, d, a.ID); got.Start != 5 || got.Status != StatusOpen {
		t.Fatalf("context search: %+v", got)
	}
	assertAnchored(t, d)
}

func TestAnchorOrphanAndRecover(t *testing.T) {
	s := newTestService(t)
	d := newEssay(t, s, "The cat sat.")
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "cat", Kind: "question", Note: "?"})[0]
	r := annotate(t, s, d.Essay.ID, AnnotateItem{ParentID: &a.ID, Note: "answer"})[0]
	if r.Start != a.Start || r.Kind != "question" {
		t.Fatalf("reply should follow parent: %+v", r)
	}

	d = setContent(t, s, d.Essay.ID, "The dog sat.")
	got := find(t, d, a.ID)
	if got.Status != StatusOrphaned || got.Start != 4 || got.End != 7 {
		t.Fatalf("expected orphaned with old offsets: %+v", got)
	}
	if find(t, d, r.ID).Status != StatusOrphaned {
		t.Fatal("reply should follow parent status")
	}

	d = setContent(t, s, d.Essay.ID, "A big cat sat.")
	got = find(t, d, a.ID)
	if got.Status != StatusOpen || got.Start != 6 || got.End != 9 {
		t.Fatalf("expected recovered: %+v", got)
	}
	if rr := find(t, d, r.ID); rr.Status != StatusOpen || rr.Start != 6 {
		t.Fatalf("reply should recover: %+v", rr)
	}
}

func TestAnchorUTF16(t *testing.T) {
	s := newTestService(t)
	text := "我爱😀 Go 语言，Go is fun 🎉!"
	d := newEssay(t, s, text)
	// 我 爱 😀(2) 空格 G o 空格 语 言 ， = 11 个码元
	a := annotate(t, s, d.Essay.ID, AnnotateItem{Quote: "Go is fun", Kind: "highlight"})[0]
	if a.Start != 11 || a.End != 20 {
		t.Fatalf("utf16 offsets: %+v", a)
	}
	start, end := 2, 4
	e := annotate(t, s, d.Essay.ID, AnnotateItem{Start: &start, End: &end, Kind: "good"})[0]
	if e.Quote != "😀" || e.Prefix != "我爱" {
		t.Fatalf("emoji range: %+v", e)
	}
	bad := 3
	res, _ := s.Annotate("user", d.Essay.ID, []AnnotateItem{{Start: &start, End: &bad, Kind: "good"}})
	if res[0].Error == "" {
		t.Fatal("range splitting a surrogate pair should fail")
	}

	d = setContent(t, s, d.Essay.ID, "🎉🎉"+text)
	if got := find(t, d, a.ID); got.Start != 15 || got.End != 24 {
		t.Fatalf("after emoji insert: %+v", got)
	}
	if got := find(t, d, e.ID); got.Start != 6 || got.Quote != "😀" {
		t.Fatalf("emoji after insert: %+v", got)
	}
	assertAnchored(t, d)
}

func TestAnchorContextLimit(t *testing.T) {
	tx := u16(strings.Repeat("ab", 40) + "X" + strings.Repeat("😀", 20))
	var a Annotation
	setAnchor(&a, tx, 80, 81)
	if len(u16(a.Prefix)) != anchorContext || a.Quote != "X" {
		t.Fatalf("prefix: %q", a.Prefix)
	}
	// 32 个码元恰好是 16 个 emoji，不切开代理对
	if a.Suffix != strings.Repeat("😀", 16) {
		t.Fatalf("suffix: %q", a.Suffix)
	}
	if setAnchor(&a, u16("ab😀"+strings.Repeat("c", 31)+"Y"), 35, 36); a.Prefix != strings.Repeat("c", 31) {
		t.Fatalf("prefix should not start mid-pair: %q", a.Prefix)
	}
}

func TestReanchorUnit(t *testing.T) {
	old := "one two three two"
	ot := u16(old)
	var a Annotation
	a.ID = "a"
	setAnchor(&a, ot, 14, 17) // 第二个 two
	a.Status = StatusOpen
	cur := "one two three, and two"
	anns := []Annotation{a}
	changed := reanchor(anns, cur, []textEdit{diffEdit(ot, u16(cur))})
	if len(changed) != 1 || anns[0].Start != 19 {
		t.Fatalf("reanchor: %+v", anns[0])
	}
}
