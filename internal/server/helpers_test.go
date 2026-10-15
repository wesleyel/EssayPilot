package server

import (
	"testing"

	"github.com/wesleyel/essay-eval/internal/core"
)

func newTestService(t *testing.T) *core.Service {
	t.Helper()
	s, err := core.NewService(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newEssay(t *testing.T, s *core.Service, content string) core.EssayDetail {
	t.Helper()
	d, err := s.CreateEssay("user", core.CreateEssayInput{
		Subject: core.English, Type: "part-b", Category: "图画·人生态度", Tags: []string{"测试"}, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ptrTo[T any](v T) *T { return &v }
