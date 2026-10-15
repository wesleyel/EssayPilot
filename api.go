package main

// REST API（/api/...）与 SSE 事件流。请求/响应均为 JSON，错误为 {"error": "..."}。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type APIOptions struct {
	MCPEnabled bool
	MCPPath    string
	WebMCP     bool
}

// ssePing 是 SSE 心跳间隔。
var ssePing = 25 * time.Second

type api struct {
	s    *Service
	opts APIOptions
}

func newAPI(s *Service, opts APIOptions) http.Handler {
	a := &api{s, opts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", a.meta)
	mux.HandleFunc("GET /api/essays", a.listEssays)
	mux.HandleFunc("POST /api/essays", a.createEssay)
	mux.HandleFunc("GET /api/essays/{id}", a.getEssay)
	mux.HandleFunc("PATCH /api/essays/{id}", a.updateEssay)
	mux.HandleFunc("DELETE /api/essays/{id}", a.deleteEssay)
	mux.HandleFunc("POST /api/essays/{id}/versions", a.saveVersion)
	mux.HandleFunc("POST /api/essays/{id}/versions/{vid}/restore", a.restoreVersion)
	mux.HandleFunc("DELETE /api/essays/{id}/versions/{vid}", a.deleteVersion)
	mux.HandleFunc("POST /api/essays/{id}/annotations", a.annotate)
	mux.HandleFunc("POST /api/essays/{id}/apply", a.applySuggestions)
	mux.HandleFunc("POST /api/essays/{id}/evaluations", a.submitEvaluation)
	mux.HandleFunc("DELETE /api/essays/{id}/evaluations/{eid}", a.deleteEvaluation)
	mux.HandleFunc("PATCH /api/annotations/{aid}", a.updateAnnotation)
	mux.HandleFunc("DELETE /api/annotations/{aid}", a.deleteAnnotation)
	mux.HandleFunc("POST /api/annotations/{aid}/apply", a.applySuggestion)
	mux.HandleFunc("GET /api/rubric/{type}", a.rubric)
	mux.HandleFunc("GET /api/prompts/{name}", a.prompt)
	mux.HandleFunc("GET /api/ui-context", a.getUIContext)
	mux.HandleFunc("PUT /api/ui-context", a.putUIContext)
	mux.HandleFunc("GET /api/activity", a.activity)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})
	return mux
}

// ---------- 工具函数 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func fail(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	default:
		log.Printf("api: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// reply 根据 err 写错误或结果。
func reply(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, status, v)
}

func noContent(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode 解析请求体；空请求体视为 {}。
func decode(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return validationf("请求体不是合法 JSON：%v", err)
}

// actor 读取 X-Actor 头：缺省 user，只接受 user 或 agent:<name>。
func actorOf(r *http.Request) (string, error) {
	a := strings.TrimSpace(r.Header.Get("X-Actor"))
	switch {
	case a == "":
		return "user", nil
	case a == "user", strings.HasPrefix(a, "agent:") && len(a) > len("agent:"):
		return a, nil
	}
	return "", validationf("X-Actor 只能是 user 或 agent:<名称>")
}

// write 包装写操作：解析 actor，Agent（WebMCP）发起的写操作记入 Agent 动态。
func (a *api) write(w http.ResponseWriter, r *http.Request, essayID string, fn func(actor string) error) {
	actor, err := actorOf(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := fn(actor); err == nil && strings.HasPrefix(actor, "agent:") {
		a.s.Log(actor, r.Method+" "+r.URL.Path, essayID, "通过 REST 调用")
	}
}

// ---------- 路由 ----------

func (a *api) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"types":                Types,
		"dimensions":           Dimensions,
		"dimensionKeys":        DimensionKeys,
		"correctionKinds":      CorrectionKinds,
		"correctionLabels":     CorrectionLabels,
		"annotationKinds":      AnnotationKinds,
		"annotationKindLabels": AnnotationKindLabels,
		"mcp":                  map[string]any{"enabled": a.opts.MCPEnabled, "path": a.opts.MCPPath},
		"webmcp":               a.opts.WebMCP,
	})
}

func (a *api) listEssays(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := a.s.ListEssays(EssayFilter{Subject: q.Get("subject"), Type: q.Get("type"), Tag: q.Get("tag"), Query: q.Get("q")})
	reply(w, http.StatusOK, list, err)
}

func (a *api) createEssay(w http.ResponseWriter, r *http.Request) {
	var in CreateEssayInput
	a.write(w, r, "", func(actor string) error {
		err := decode(r, &in)
		var d EssayDetail
		if err == nil {
			d, err = a.s.CreateEssay(actor, in)
		}
		reply(w, http.StatusCreated, d, err)
		return err
	})
}

func (a *api) getEssay(w http.ResponseWriter, r *http.Request) {
	d, err := a.s.GetEssay(r.PathValue("id"))
	reply(w, http.StatusOK, d, err)
}

func (a *api) updateEssay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		var in UpdateEssayInput
		err := decode(r, &in)
		var d EssayDetail
		if err == nil {
			d, err = a.s.UpdateEssay(actor, id, in)
		}
		reply(w, http.StatusOK, d, err)
		return err
	})
}

func (a *api) deleteEssay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		err := a.s.DeleteEssay(actor, id)
		noContent(w, err)
		return err
	})
}

func (a *api) saveVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		var in struct {
			Note string `json:"note"`
		}
		err := decode(r, &in)
		var v Version
		if err == nil {
			v, err = a.s.SaveVersion(actor, id, in.Note)
		}
		reply(w, http.StatusCreated, v, err)
		return err
	})
}

func (a *api) restoreVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		d, err := a.s.RestoreVersion(actor, id, r.PathValue("vid"))
		reply(w, http.StatusOK, d, err)
		return err
	})
}

func (a *api) deleteVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		err := a.s.DeleteVersion(actor, id, r.PathValue("vid"))
		noContent(w, err)
		return err
	})
}

func (a *api) annotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		var in struct {
			Items []AnnotateItem `json:"items"`
		}
		err := decode(r, &in)
		var res []AnnotateResult
		if err == nil {
			res, err = a.s.Annotate(actor, id, in.Items)
		}
		reply(w, http.StatusOK, res, err)
		return err
	})
}

func (a *api) applySuggestions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		var in struct {
			IDs []string `json:"ids"`
		}
		err := decode(r, &in)
		var d EssayDetail
		if err == nil {
			d, err = a.s.ApplySuggestions(actor, id, in.IDs)
		}
		reply(w, http.StatusOK, d, err)
		return err
	})
}

func (a *api) submitEvaluation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		var in EvaluationInput
		err := decode(r, &in)
		var res EvaluationResult
		if err == nil {
			res, err = a.s.SubmitEvaluation(actor, id, in)
		}
		reply(w, http.StatusCreated, res, err)
		return err
	})
}

func (a *api) deleteEvaluation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.write(w, r, id, func(actor string) error {
		err := a.s.DeleteEvaluation(actor, id, r.PathValue("eid"))
		noContent(w, err)
		return err
	})
}

func (a *api) updateAnnotation(w http.ResponseWriter, r *http.Request) {
	a.write(w, r, "", func(actor string) error {
		var p AnnotationPatch
		err := decode(r, &p)
		var ann Annotation
		if err == nil {
			ann, err = a.s.UpdateAnnotation(actor, r.PathValue("aid"), p)
		}
		reply(w, http.StatusOK, ann, err)
		return err
	})
}

func (a *api) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	a.write(w, r, "", func(actor string) error {
		err := a.s.DeleteAnnotation(actor, r.PathValue("aid"))
		noContent(w, err)
		return err
	})
}

func (a *api) applySuggestion(w http.ResponseWriter, r *http.Request) {
	a.write(w, r, "", func(actor string) error {
		d, err := a.s.ApplySuggestion(actor, r.PathValue("aid"))
		reply(w, http.StatusOK, d, err)
		return err
	})
}

func (a *api) rubric(w http.ResponseWriter, r *http.Request) {
	spec, ok := typeSpec(r.PathValue("type"))
	if !ok {
		writeError(w, http.StatusNotFound, "题型不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spec": spec, "text": rubric(spec)})
}

func (a *api) prompt(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("essay")
	if id == "" {
		fail(w, validationf("缺少 essay 参数"))
		return
	}
	d, err := a.s.GetEssay(id)
	if err != nil {
		fail(w, err)
		return
	}
	text, err := promptText(r.PathValue("name"), d)
	if err != nil {
		if !isInputError(err) {
			err = invalid(err)
		}
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (a *api) getUIContext(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.s.UIContext())
}

func (a *api) putUIContext(w http.ResponseWriter, r *http.Request) {
	var c UIContext
	if err := decode(r, &c); err != nil {
		fail(w, err)
		return
	}
	a.s.SetUIContext(c)
	writeJSON(w, http.StatusOK, a.s.UIContext())
}

func (a *api) activity(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fail(w, validationf("limit 须为正整数"))
			return
		}
		limit = n
	}
	writeJSON(w, http.StatusOK, a.s.Activity(limit))
}

// events：SSE。每条为 "event: <type>\ndata: <Event JSON>\n\n"，每 25s 一条 ": ping"。
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	ch, cancel := a.s.Subscribe()
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	if err := rc.Flush(); err != nil {
		return
	}
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
