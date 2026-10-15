package core

// Service：题目、版本、评分、标注的全部业务规则。REST 与 MCP（internal/server）共用。
// 写操作由 mu 串行化；每次写操作成功后广播对应 Event。

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("未找到")

// ValidationError 表示输入不合法（REST 映射为 400）。
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func Validationf(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Invalid 把 model.go 里的普通校验错误转成 ValidationError。
func Invalid(err error) error {
	if err == nil {
		return nil
	}
	return &ValidationError{Msg: err.Error()}
}

type Service struct {
	db  *sql.DB
	mu  sync.Mutex
	hub *hub

	uiMu sync.Mutex
	ui   UIContext
}

func NewService(dbPath string) (*Service, error) {
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, hub: newHub()}, nil
}

func (s *Service) Close() error { return s.db.Close() }

func (s *Service) emit(actor, essayID string, types ...string) {
	for _, t := range types {
		s.hub.publish(Event{Type: t, EssayID: essayID, Actor: actor})
	}
}

func (s *Service) tx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---------- 题目 ----------

func (s *Service) ListEssays(f EssayFilter) ([]EssaySummary, error) {
	rows, err := s.db.Query(`SELECT e.id, e.subject, e.type, e.title, e.category, e.tags, e.word_count, e.updated_at,
		(SELECT count(*) FROM annotations a WHERE a.essay_id = e.id AND a.parent_id IS NULL AND a.status != 'resolved'),
		(SELECT count(*) FROM versions v WHERE v.essay_id = e.id)
		FROM essays e
		WHERE (?1 = '' OR e.subject = ?1) AND (?2 = '' OR e.type = ?2)
		  AND (?3 = '' OR EXISTS (SELECT 1 FROM json_each(e.tags) WHERE value = ?3))
		  AND (?4 = '' OR instr(lower(e.title || char(10) || e.prompt || char(10) || e.content), lower(?4)) > 0)
		ORDER BY e.updated_at DESC, e.rowid DESC`,
		f.Subject, f.Type, strings.TrimSpace(f.Tag), strings.TrimSpace(f.Query))
	if err != nil {
		return nil, err
	}
	out := []EssaySummary{}
	for rows.Next() {
		var e EssaySummary
		var tags string
		if err := rows.Scan(&e.ID, &e.Subject, &e.Type, &e.Title, &e.Category, &tags, &e.WordCount, &e.UpdatedAt,
			&e.AnnotationCount, &e.VersionCount); err != nil {
			rows.Close()
			return nil, err
		}
		e.Tags = []string{}
		_ = jsonUnmarshal(tags, &e.Tags)
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	latest := map[string]*ScoreSummary{}
	rows, err = s.db.Query(`SELECT essay_id, score, max_score, band, created_at FROM evaluations ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var sc ScoreSummary
		if err := rows.Scan(&id, &sc.Score, &sc.MaxScore, &sc.Band, &sc.CreatedAt); err != nil {
			return nil, err
		}
		latest[id] = &sc
	}
	for i := range out {
		out[i].LatestScore = latest[out[i].ID]
	}
	return out, rows.Err()
}

func (s *Service) GetEssay(id string) (EssayDetail, error) {
	var d EssayDetail
	var err error
	if d.Essay, err = getEssay(s.db, id); err != nil {
		return d, err
	}
	if d.Annotations, err = listAnnotations(s.db, id); err != nil {
		return d, err
	}
	if d.Evaluations, err = listEvaluations(s.db, id); err != nil {
		return d, err
	}
	d.Versions, err = listVersions(s.db, id)
	return d, err
}

func cleanTitle(t string) string {
	if t = strings.TrimSpace(t); t == "" {
		return "未命名"
	}
	return t
}

func cleanTags(tags []string) ([]string, error) {
	tags = normalizeTags(tags)
	return tags, Invalid(tagsError(tags))
}

func (s *Service) CreateEssay(actor string, in CreateEssayInput) (EssayDetail, error) {
	if !validSubject(in.Subject) {
		return EssayDetail{}, Validationf("科目无效：%q（可选 english / politics）", in.Subject)
	}
	spec, ok := LookupType(in.Type)
	if !ok || spec.Subject != in.Subject {
		return EssayDetail{}, Validationf("题型 %q 不属于科目 %s", in.Type, in.Subject)
	}
	category := strings.TrimSpace(in.Category)
	if err := categoryError(spec, category); err != nil {
		return EssayDetail{}, Invalid(err)
	}
	tags, err := cleanTags(in.Tags)
	if err != nil {
		return EssayDetail{}, err
	}
	at := now()
	e := Essay{
		ID: newID(), Subject: in.Subject, Type: spec.ID, Title: cleanTitle(in.Title), Category: category, Tags: tags,
		Prompt: in.Prompt, PromptImage: in.PromptImage, Content: in.Content,
		WordCount: countWords(in.Content, in.Subject), CreatedAt: at, UpdatedAt: at,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := insertEssay(s.db, e); err != nil {
		return EssayDetail{}, err
	}
	s.emit(actor, e.ID, "essay")
	return s.GetEssay(e.ID)
}

func (s *Service) UpdateEssay(actor, id string, in UpdateEssayInput) (EssayDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, id)
	if err != nil {
		return EssayDetail{}, err
	}
	old := e.Content
	spec, _ := LookupType(e.Type)
	if in.Type != nil && *in.Type != e.Type {
		ns, ok := LookupType(*in.Type)
		if !ok || ns.Subject != e.Subject {
			return EssayDetail{}, Validationf("题型只能在本科目（%s）内切换，%q 不可用", e.Subject, *in.Type)
		}
		spec, e.Type = ns, ns.ID
	}
	if in.Category != nil {
		e.Category = strings.TrimSpace(*in.Category)
	}
	if in.Type != nil || in.Category != nil {
		if err := categoryError(spec, e.Category); err != nil {
			return EssayDetail{}, Invalid(err)
		}
	}
	if in.Tags != nil {
		if e.Tags, err = cleanTags(*in.Tags); err != nil {
			return EssayDetail{}, err
		}
	}
	if in.Title != nil {
		e.Title = cleanTitle(*in.Title)
	}
	if in.Prompt != nil {
		e.Prompt = *in.Prompt
	}
	if in.PromptImage != nil {
		e.PromptImage = *in.PromptImage
	}
	if in.Content != nil {
		e.Content = *in.Content
		e.WordCount = countWords(e.Content, e.Subject)
	}
	e.UpdatedAt = now()
	moved := false
	err = s.tx(func(tx *sql.Tx) error {
		if err := saveEssay(tx, e); err != nil {
			return err
		}
		if e.Content != old {
			moved, err = reanchorEssay(tx, e.ID, e.Content, []textEdit{diffEdit(u16(old), u16(e.Content))}, e.UpdatedAt)
		}
		return err
	})
	if err != nil {
		return EssayDetail{}, err
	}
	s.emit(actor, id, "essay")
	if moved {
		s.emit(actor, id, "annotations")
	}
	return s.GetEssay(id)
}

// reanchorEssay 在正文改动后重新锚定该篇全部标注并写回有变化的。
func reanchorEssay(tx *sql.Tx, essayID, content string, edits []textEdit, at string) (bool, error) {
	anns, err := listAnnotations(tx, essayID)
	if err != nil {
		return false, err
	}
	changed := reanchor(anns, content, edits)
	for _, i := range changed {
		anns[i].UpdatedAt = at
		if err := saveAnnotation(tx, anns[i]); err != nil {
			return false, err
		}
	}
	return len(changed) > 0, nil
}

func (s *Service) DeleteEssay(actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM essays WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.emit(actor, id, "deleted")
	return nil
}

// ---------- 版本 ----------

func snapshotOf(e Essay, note, actor, at string) Version {
	return Version{
		ID: newID(), EssayID: e.ID, Note: note, Title: e.Title, Type: e.Type, Prompt: e.Prompt,
		Content: e.Content, WordCount: e.WordCount, Author: actor, CreatedAt: at,
	}
}

func (s *Service) SaveVersion(actor, essayID, note string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, essayID)
	if err != nil {
		return Version{}, err
	}
	if note = strings.TrimSpace(note); note == "" {
		note = "手动保存"
	}
	v := snapshotOf(e, note, actor, now())
	if err := insertVersion(s.db, v); err != nil {
		return Version{}, err
	}
	s.emit(actor, essayID, "versions")
	return v, nil
}

func (s *Service) versionOf(essayID, versionID string) (Version, error) {
	v, err := getVersion(s.db, versionID)
	if err == nil && v.EssayID != essayID {
		err = ErrNotFound
	}
	return v, err
}

func (s *Service) RestoreVersion(actor, essayID, versionID string) (EssayDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, essayID)
	if err != nil {
		return EssayDetail{}, err
	}
	v, err := s.versionOf(essayID, versionID)
	if err != nil {
		return EssayDetail{}, err
	}
	at := now()
	backup := snapshotOf(e, "恢复前自动保存", actor, at)
	old := e.Content
	e.Title, e.Type, e.Prompt, e.Content = v.Title, v.Type, v.Prompt, v.Content
	e.WordCount = countWords(e.Content, e.Subject)
	if spec, ok := LookupType(e.Type); ok && categoryError(spec, e.Category) != nil {
		e.Category = "" // 题型变了而原分类不再适用：清空，由用户重选
	}
	e.UpdatedAt = at
	err = s.tx(func(tx *sql.Tx) error {
		if err := insertVersion(tx, backup); err != nil {
			return err
		}
		if err := saveEssay(tx, e); err != nil {
			return err
		}
		_, err := reanchorEssay(tx, e.ID, e.Content, []textEdit{diffEdit(u16(old), u16(e.Content))}, at)
		return err
	})
	if err != nil {
		return EssayDetail{}, err
	}
	s.emit(actor, essayID, "essay", "versions", "annotations")
	return s.GetEssay(essayID)
}

func (s *Service) DeleteVersion(actor, essayID, versionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.versionOf(essayID, versionID); err != nil {
		return err
	}
	// 外键级联：评分随版本删除，评分名下标注的 evaluation_id 置空
	if _, err := s.db.Exec(`DELETE FROM versions WHERE id = ?`, versionID); err != nil {
		return err
	}
	s.emit(actor, essayID, "versions", "evaluations", "annotations")
	return nil
}

// ---------- 标注 ----------

// buildAnnotation 校验一条 AnnotateItem 并定位到正文 t 上。
func buildAnnotation(q querier, e Essay, t []uint16, actor string, it AnnotateItem, evalID *string, at string) (Annotation, error) {
	a := Annotation{
		ID: newID(), EssayID: e.ID, EvaluationID: evalID, Kind: strings.TrimSpace(it.Kind),
		Label: strings.TrimSpace(it.Label), Note: it.Note, Suggestion: it.Suggestion,
		Author: actor, Status: StatusOpen, CreatedAt: at, UpdatedAt: at,
	}
	if it.ParentID != nil {
		p, err := getAnnotation(q, *it.ParentID)
		if err == nil && p.ParentID != nil { // 回复的回复挂到根标注上
			p, err = getAnnotation(q, *p.ParentID)
		}
		if errors.Is(err, ErrNotFound) || (err == nil && p.EssayID != e.ID) {
			return a, Validationf("父标注 %s 不存在或不属于这篇作文", *it.ParentID)
		}
		if err != nil {
			return a, err
		}
		a.ParentID = &p.ID
		if a.Kind == "" {
			a.Kind = p.Kind
		}
		followParent(&a, p)
	}
	if !slices.Contains(AnnotationKinds, a.Kind) {
		return a, Validationf("kind 须为 %s 之一", strings.Join(AnnotationKinds, " / "))
	}
	if a.ParentID != nil {
		return a, nil
	}
	if a.Kind == "correction" && a.Suggestion == nil {
		return a, Validationf("纠错标注（correction）必须给出 suggestion")
	}
	switch {
	case it.Start != nil && it.End != nil:
		if err := checkRange(t, *it.Start, *it.End); err != nil {
			return a, err
		}
		if got := s16(t[*it.Start:*it.End]); it.Quote != "" && got != it.Quote {
			return a, Validationf("区间 [%d,%d) 的文本是「%s」，与 quote「%s」不符", *it.Start, *it.End, got, it.Quote)
		}
		setAnchor(&a, t, *it.Start, *it.End)
	case it.Start != nil || it.End != nil:
		return a, Validationf("start 与 end 须同时给出")
	default:
		start, end, err := locateQuote(t, it.Quote, it.Occurrence)
		if err != nil {
			return a, err
		}
		setAnchor(&a, t, start, end)
	}
	return a, nil
}

func IsInputError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve) || errors.Is(err, ErrNotFound)
}

func (s *Service) Annotate(actor, essayID string, items []AnnotateItem) ([]AnnotateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, essayID)
	if err != nil {
		return nil, err
	}
	t, at := u16(e.Content), now()
	results := make([]AnnotateResult, len(items))
	ok := 0
	err = s.tx(func(tx *sql.Tx) error {
		for i, it := range items {
			results[i].Index = i
			a, err := buildAnnotation(tx, e, t, actor, it, nil, at)
			if IsInputError(err) {
				results[i].Error = err.Error()
				continue
			}
			if err == nil {
				err = insertAnnotation(tx, a)
			}
			if err != nil {
				return err
			}
			results[i].Annotation = &a
			ok++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ok > 0 {
		s.emit(actor, essayID, "annotations")
	}
	return results, nil
}

func (s *Service) ListAnnotations(essayID string, f AnnotationFilter) ([]Annotation, error) {
	if _, err := getEssay(s.db, essayID); err != nil {
		return nil, err
	}
	all, err := listAnnotations(s.db, essayID)
	if err != nil {
		return nil, err
	}
	out := []Annotation{}
	for _, a := range all {
		switch {
		case f.Author == "agent" && !strings.HasPrefix(a.Author, "agent:"),
			f.Author != "" && f.Author != "agent" && a.Author != f.Author,
			f.Kind != "" && a.Kind != f.Kind,
			f.Status != "" && a.Status != f.Status:
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// ListHighlights 列出全部作文里仍挂在正文上的重点（kind=highlight、顶层、状态 open），
// 按作文更新时间倒序、同篇按位置排列。
func (s *Service) ListHighlights() ([]HighlightItem, error) {
	rows, err := s.db.Query(`SELECT `+prefixCols("a.", annotationCols)+`, e.title, e.subject, e.type, e.category, e.tags
		FROM annotations a JOIN essays e ON e.id = a.essay_id
		WHERE a.kind = 'highlight' AND a.parent_id IS NULL AND a.status = 'open'
		ORDER BY e.updated_at DESC, e.rowid DESC, a.start_pos, a.created_at, a.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HighlightItem{}
	for rows.Next() {
		var h HighlightItem
		var tags string
		var eid, pid, sug sql.NullString
		a := &h.Annotation
		if err := rows.Scan(&a.ID, &a.EssayID, &eid, &pid, &a.Start, &a.End, &a.Quote, &a.Prefix, &a.Suffix,
			&a.Kind, &a.Label, &a.Note, &sug, &a.Author, &a.Status, &a.CreatedAt, &a.UpdatedAt,
			&h.EssayTitle, &h.EssaySubject, &h.EssayType, &h.EssayCategory, &tags); err != nil {
			return nil, err
		}
		a.EvaluationID, a.ParentID, a.Suggestion = strPtr(eid), strPtr(pid), strPtr(sug)
		h.EssayTags = []string{}
		_ = jsonUnmarshal(tags, &h.EssayTags)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Service) UpdateAnnotation(actor, id string, p AnnotationPatch) (Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := getAnnotation(s.db, id)
	if err != nil {
		return a, err
	}
	e, err := getEssay(s.db, a.EssayID)
	if err != nil {
		return a, err
	}
	t := u16(e.Content)
	reply := a.ParentID != nil
	if p.Kind != nil {
		if !slices.Contains(AnnotationKinds, *p.Kind) {
			return a, Validationf("kind 须为 %s 之一", strings.Join(AnnotationKinds, " / "))
		}
		a.Kind = *p.Kind
	}
	if p.Label != nil {
		a.Label = strings.TrimSpace(*p.Label)
	}
	if p.Note != nil {
		a.Note = *p.Note
	}
	if p.Suggestion != nil {
		a.Suggestion = p.Suggestion
	}
	if !reply && a.Kind == "correction" && a.Suggestion == nil {
		return a, Validationf("纠错标注（correction）必须给出 suggestion")
	}
	if p.Status != nil {
		if reply {
			return a, Validationf("回复的状态跟随父标注")
		}
		switch *p.Status {
		case StatusResolved:
			a.Status = StatusResolved
		case StatusOpen:
			if a.Status != StatusOpen { // 重新打开：按当前正文重新定位
				a.Status = StatusOpen
				relocate(&a, t, nil)
			}
		default:
			return a, Validationf("status 只能设为 open 或 resolved")
		}
	}
	if p.Start != nil || p.End != nil {
		if reply {
			return a, Validationf("回复跟随父标注的区间，不能单独定位")
		}
		if p.Start == nil || p.End == nil {
			return a, Validationf("start 与 end 须同时给出")
		}
		if err := checkRange(t, *p.Start, *p.End); err != nil {
			return a, err
		}
		setAnchor(&a, t, *p.Start, *p.End)
		a.Status = StatusOpen
	}
	a.UpdatedAt = now()
	err = s.tx(func(tx *sql.Tx) error {
		if err := saveAnnotation(tx, a); err != nil {
			return err
		}
		if reply {
			return nil
		}
		anns, err := listAnnotations(tx, a.EssayID)
		if err != nil {
			return err
		}
		for _, r := range anns {
			if r.ParentID != nil && *r.ParentID == a.ID {
				followParent(&r, a)
				r.UpdatedAt = a.UpdatedAt
				if err := saveAnnotation(tx, r); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return a, err
	}
	s.emit(actor, a.EssayID, "annotations")
	return a, nil
}

func (s *Service) DeleteAnnotation(actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := getAnnotation(s.db, id)
	if err != nil {
		return err
	}
	// 回复经外键级联删除
	if _, err := s.db.Exec(`DELETE FROM annotations WHERE id = ?`, id); err != nil {
		return err
	}
	s.emit(actor, a.EssayID, "annotations")
	return nil
}

func (s *Service) ApplySuggestion(actor, id string) (EssayDetail, error) {
	a, err := getAnnotation(s.db, id)
	if err != nil {
		return EssayDetail{}, err
	}
	return s.ApplySuggestions(actor, a.EssayID, []string{id})
}

// ApplySuggestions 用建议替换正文；ids 为空表示全部 open 且有 suggestion 的顶层标注。
// 区间重叠时只采纳靠前的一条。被采纳的标注改为 resolved，区间改为替换后的文本，其余重新锚定。
func (s *Service) ApplySuggestions(actor, essayID string, ids []string) (EssayDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, essayID)
	if err != nil {
		return EssayDetail{}, err
	}
	anns, err := listAnnotations(s.db, essayID)
	if err != nil {
		return EssayDetail{}, err
	}
	t := u16(e.Content)
	applicable := func(a Annotation) error {
		switch {
		case a.ParentID != nil:
			return Validationf("标注 %s 是回复，不能采纳", a.ID)
		case a.Suggestion == nil:
			return Validationf("标注 %s 没有替换建议", a.ID)
		case a.Status != StatusOpen:
			return Validationf("标注 %s 状态为 %s，不能采纳", a.ID, a.Status)
		case a.End > len(t) || a.Start >= a.End || s16(t[a.Start:a.End]) != a.Quote:
			return Validationf("标注 %s 的位置与正文不符，请先重新定位", a.ID)
		}
		return nil
	}
	var picked []int
	if len(ids) == 0 {
		for i, a := range anns {
			if applicable(a) == nil {
				picked = append(picked, i)
			}
		}
	} else {
		index := map[string]int{}
		for i, a := range anns {
			index[a.ID] = i
		}
		for _, id := range ids {
			i, ok := index[id]
			if !ok {
				return EssayDetail{}, Validationf("标注 %s 不属于这篇作文", id)
			}
			if err := applicable(anns[i]); err != nil {
				return EssayDetail{}, err
			}
			if !slices.Contains(picked, i) {
				picked = append(picked, i)
			}
		}
	}
	sort.Slice(picked, func(x, y int) bool { return anns[picked[x]].Start < anns[picked[y]].Start })
	var reps []replacement
	var applied []int
	lastEnd := -1
	for _, i := range picked {
		a := anns[i]
		if a.Start < lastEnd {
			continue
		}
		reps = append(reps, replacement{a.Start, a.End, *a.Suggestion})
		applied = append(applied, i)
		lastEnd = a.End
	}
	if len(reps) == 0 {
		return EssayDetail{}, Validationf("没有可采纳的建议")
	}

	content, edits, ranges := applyReplacements(t, reps)
	at := now()
	for k, i := range applied {
		anns[i].Status = StatusResolved
		anns[i].Start, anns[i].End = ranges[k][0], ranges[k][1]
		anns[i].UpdatedAt = at
	}
	changed := append(reanchor(anns, content, edits), applied...)
	e.Content = content
	e.WordCount = countWords(content, e.Subject)
	e.UpdatedAt = at
	err = s.tx(func(tx *sql.Tx) error {
		if err := saveEssay(tx, e); err != nil {
			return err
		}
		for _, i := range changed {
			anns[i].UpdatedAt = at
			if err := saveAnnotation(tx, anns[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return EssayDetail{}, err
	}
	s.emit(actor, essayID, "essay", "annotations")
	return s.GetEssay(essayID)
}

// ---------- 评分 ----------

func (s *Service) SubmitEvaluation(actor, essayID string, in EvaluationInput) (EvaluationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := getEssay(s.db, essayID)
	if err != nil {
		return EvaluationResult{}, err
	}
	if strings.TrimSpace(e.Content) == "" {
		return EvaluationResult{}, Validationf("正文为空，无法评分")
	}
	spec, _ := LookupType(e.Type)
	if len(in.Dimensions) != len(DimensionKeys) {
		return EvaluationResult{}, Validationf("dimensions 须恰好包含 %s 四项", strings.Join(DimensionKeys, " / "))
	}
	dims := map[string]DimensionScore{}
	sum := 0.0
	for _, key := range DimensionKeys {
		d, ok := in.Dimensions[key]
		if !ok {
			return EvaluationResult{}, Validationf("dimensions 缺少 %s（须恰好包含 %s）", key, strings.Join(DimensionKeys, " / "))
		}
		dmax := DimensionMax(e.Subject, spec.MaxScore, key)
		if d.Score < 0 || d.Score > dmax+1e-9 {
			return EvaluationResult{}, Validationf("维度 %s 得分 %g 超出范围 0–%g", key, d.Score, dmax)
		}
		dims[key] = DimensionScore{Score: d.Score, MaxScore: dmax, Feedback: d.Feedback}
		sum += d.Score
	}
	if in.Score < 0 || in.Score > spec.MaxScore {
		return EvaluationResult{}, Validationf("总分 %g 超出范围 0–%g", in.Score, spec.MaxScore)
	}
	if math.Abs(sum-in.Score) > 0.05+1e-9 {
		return EvaluationResult{}, Validationf("四个维度之和 %.2f 与总分 %g 不符", sum, in.Score)
	}
	kinds := CorrectionKinds[e.Subject]
	for i, c := range in.Corrections {
		if !slices.Contains(kinds, c.Kind) {
			return EvaluationResult{}, Validationf("第 %d 条纠错的 kind %q 无效，可选：%s", i+1, c.Kind, strings.Join(kinds, " / "))
		}
	}

	at := now()
	score := in.Score
	evalID := newID()
	v := snapshotOf(e, "评分快照", actor, at)
	v.EvaluationID, v.Score = &evalID, &score
	ev := Evaluation{
		ID: evalID, EssayID: e.ID, VersionID: v.ID, Score: score, MaxScore: spec.MaxScore, Band: in.Band,
		Dimensions: dims, OverallComment: in.OverallComment, Strengths: orEmpty(in.Strengths),
		Weaknesses: orEmpty(in.Weaknesses), Polished: in.Polished, Author: actor, CreatedAt: at,
	}
	t := u16(e.Content)
	results := make([]AnnotateResult, len(in.Corrections))
	err = s.tx(func(tx *sql.Tx) error {
		if err := insertVersion(tx, v); err != nil {
			return err
		}
		if err := insertEvaluation(tx, ev); err != nil {
			return err
		}
		for i, c := range in.Corrections {
			results[i].Index = i
			corrected := c.Corrected
			item := AnnotateItem{
				Quote: c.Quote, Occurrence: c.Occurrence, Kind: "correction",
				Label: CorrectionLabels[c.Kind], Note: c.Explanation, Suggestion: &corrected,
			}
			a, err := buildAnnotation(tx, e, t, actor, item, &evalID, at)
			if IsInputError(err) {
				results[i].Error = err.Error()
				continue
			}
			if err == nil {
				err = insertAnnotation(tx, a)
			}
			if err != nil {
				return err
			}
			results[i].Annotation = &a
		}
		return touchEssay(tx, e.ID, at)
	})
	if err != nil {
		return EvaluationResult{}, err
	}
	s.emit(actor, essayID, "versions", "evaluations", "annotations")
	return EvaluationResult{Evaluation: ev, Version: v, Corrections: results}, nil
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func (s *Service) DeleteEvaluation(actor, essayID, evaluationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, err := getEvaluation(s.db, evaluationID)
	if err == nil && ev.EssayID != essayID {
		err = ErrNotFound
	}
	if err != nil {
		return err
	}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM evaluations WHERE id = ?`, evaluationID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE versions SET evaluation_id = NULL, score = NULL WHERE evaluation_id = ?`, evaluationID)
		return err
	})
	if err != nil {
		return err
	}
	s.emit(actor, essayID, "evaluations", "versions", "annotations")
	return nil
}

// ---------- 界面上下文与 Agent 动态 ----------

func (s *Service) SetUIContext(c UIContext) {
	c.UpdatedAt = now()
	s.uiMu.Lock()
	s.ui = c
	s.uiMu.Unlock()
	s.hub.publish(Event{Type: "ui-context", EssayID: c.EssayID, Actor: "user"})
}

func (s *Service) UIContext() UIContext {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	return s.ui
}

func (s *Service) Log(actor, tool, essayID, summary string) {
	a := s.hub.record(Activity{Actor: actor, Tool: tool, EssayID: essayID, Summary: summary})
	s.hub.publish(Event{Type: "activity", EssayID: essayID, Actor: actor, Activity: &a})
}

func (s *Service) Activity(limit int) []Activity { return s.hub.recent(limit) }

func (s *Service) Subscribe() (<-chan Event, func()) { return s.hub.subscribe() }

// Subscribers 返回当前 SSE 订阅者数量。
func (s *Service) Subscribers() int {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return len(s.hub.subs)
}

// GetAnnotation 按 id 取标注。
func (s *Service) GetAnnotation(id string) (Annotation, error) { return getAnnotation(s.db, id) }
