package core

// SQLite 存储：建表、行与实体的互转。业务规则在 service.go。

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS essays (
	id           TEXT PRIMARY KEY,
	subject      TEXT NOT NULL,
	type         TEXT NOT NULL,
	title        TEXT NOT NULL,
	category     TEXT NOT NULL DEFAULT '',
	tags         TEXT NOT NULL DEFAULT '[]',
	prompt       TEXT NOT NULL DEFAULT '',
	prompt_image TEXT NOT NULL DEFAULT '',
	content      TEXT NOT NULL DEFAULT '',
	word_count   INTEGER NOT NULL DEFAULT 0,
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS versions (
	id            TEXT PRIMARY KEY,
	essay_id      TEXT NOT NULL REFERENCES essays(id) ON DELETE CASCADE,
	note          TEXT NOT NULL DEFAULT '',
	title         TEXT NOT NULL,
	type          TEXT NOT NULL,
	prompt        TEXT NOT NULL DEFAULT '',
	content       TEXT NOT NULL DEFAULT '',
	word_count    INTEGER NOT NULL DEFAULT 0,
	evaluation_id TEXT,
	score         REAL,
	author        TEXT NOT NULL,
	created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS versions_essay ON versions(essay_id);
CREATE TABLE IF NOT EXISTS evaluations (
	id              TEXT PRIMARY KEY,
	essay_id        TEXT NOT NULL REFERENCES essays(id) ON DELETE CASCADE,
	version_id      TEXT NOT NULL REFERENCES versions(id) ON DELETE CASCADE,
	score           REAL NOT NULL,
	max_score       REAL NOT NULL,
	band            TEXT NOT NULL DEFAULT '',
	dimensions      TEXT NOT NULL DEFAULT '{}',
	overall_comment TEXT NOT NULL DEFAULT '',
	strengths       TEXT NOT NULL DEFAULT '[]',
	weaknesses      TEXT NOT NULL DEFAULT '[]',
	polished        TEXT NOT NULL DEFAULT '',
	author          TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS evaluations_essay ON evaluations(essay_id);
CREATE TABLE IF NOT EXISTS annotations (
	id            TEXT PRIMARY KEY,
	essay_id      TEXT NOT NULL REFERENCES essays(id) ON DELETE CASCADE,
	evaluation_id TEXT REFERENCES evaluations(id) ON DELETE SET NULL,
	parent_id     TEXT REFERENCES annotations(id) ON DELETE CASCADE,
	start_pos     INTEGER NOT NULL,
	end_pos       INTEGER NOT NULL,
	quote         TEXT NOT NULL,
	prefix        TEXT NOT NULL DEFAULT '',
	suffix        TEXT NOT NULL DEFAULT '',
	kind          TEXT NOT NULL,
	label         TEXT NOT NULL DEFAULT '',
	note          TEXT NOT NULL DEFAULT '',
	suggestion    TEXT,
	author        TEXT NOT NULL,
	status        TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS annotations_essay ON annotations(essay_id);
CREATE INDEX IF NOT EXISTS annotations_parent ON annotations(parent_id);
`

// openDB 打开（或创建）数据库并建表。":memory:" 只用单连接，否则每个连接各是一个空库。
func openDB(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// querier 同时由 *sql.DB 与 *sql.Tx 实现。
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type scanner interface{ Scan(dest ...any) error }

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// now 用定宽纳秒格式（仍是合法 RFC3339Nano），字符串排序即时间排序。
func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// ---------- essays ----------

const essayCols = `id, subject, type, title, category, tags, prompt, prompt_image, content, word_count, created_at, updated_at`

func scanEssay(r scanner) (Essay, error) {
	var e Essay
	var tags string
	err := r.Scan(&e.ID, &e.Subject, &e.Type, &e.Title, &e.Category, &tags, &e.Prompt, &e.PromptImage,
		&e.Content, &e.WordCount, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	_ = json.Unmarshal([]byte(tags), &e.Tags)
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return e, nil
}

func getEssay(q querier, id string) (Essay, error) {
	e, err := scanEssay(q.QueryRow(`SELECT `+essayCols+` FROM essays WHERE id = ?`, id))
	return e, notFound(err)
}

func insertEssay(q querier, e Essay) error {
	_, err := q.Exec(`INSERT INTO essays (`+essayCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Subject, e.Type, e.Title, e.Category, toJSON(e.Tags), e.Prompt, e.PromptImage,
		e.Content, e.WordCount, e.CreatedAt, e.UpdatedAt)
	return err
}

func saveEssay(q querier, e Essay) error {
	_, err := q.Exec(`UPDATE essays SET type=?, title=?, category=?, tags=?, prompt=?, prompt_image=?, content=?,
		word_count=?, updated_at=? WHERE id=?`,
		e.Type, e.Title, e.Category, toJSON(e.Tags), e.Prompt, e.PromptImage, e.Content, e.WordCount, e.UpdatedAt, e.ID)
	return err
}

func touchEssay(q querier, id, at string) error {
	_, err := q.Exec(`UPDATE essays SET updated_at=? WHERE id=?`, at, id)
	return err
}

// ---------- versions ----------

const versionCols = `id, essay_id, note, title, type, prompt, content, word_count, evaluation_id, score, author, created_at`

func scanVersion(r scanner) (Version, error) {
	var v Version
	var eid sql.NullString
	var score sql.NullFloat64
	err := r.Scan(&v.ID, &v.EssayID, &v.Note, &v.Title, &v.Type, &v.Prompt, &v.Content, &v.WordCount,
		&eid, &score, &v.Author, &v.CreatedAt)
	v.EvaluationID = strPtr(eid)
	if score.Valid {
		v.Score = &score.Float64
	}
	return v, err
}

func getVersion(q querier, id string) (Version, error) {
	v, err := scanVersion(q.QueryRow(`SELECT `+versionCols+` FROM versions WHERE id = ?`, id))
	return v, notFound(err)
}

func insertVersion(q querier, v Version) error {
	var score any
	if v.Score != nil {
		score = *v.Score
	}
	_, err := q.Exec(`INSERT INTO versions (`+versionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.EssayID, v.Note, v.Title, v.Type, v.Prompt, v.Content, v.WordCount,
		nullStr(v.EvaluationID), score, v.Author, v.CreatedAt)
	return err
}

func listVersions(q querier, essayID string) ([]Version, error) {
	rows, err := q.Query(`SELECT `+versionCols+` FROM versions WHERE essay_id = ? ORDER BY created_at DESC, rowid DESC`, essayID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---------- evaluations ----------

const evaluationCols = `id, essay_id, version_id, score, max_score, band, dimensions, overall_comment, strengths, weaknesses, polished, author, created_at`

func scanEvaluation(r scanner) (Evaluation, error) {
	var e Evaluation
	var dims, strengths, weaknesses string
	err := r.Scan(&e.ID, &e.EssayID, &e.VersionID, &e.Score, &e.MaxScore, &e.Band, &dims, &e.OverallComment,
		&strengths, &weaknesses, &e.Polished, &e.Author, &e.CreatedAt)
	if err != nil {
		return e, err
	}
	_ = json.Unmarshal([]byte(dims), &e.Dimensions)
	_ = json.Unmarshal([]byte(strengths), &e.Strengths)
	_ = json.Unmarshal([]byte(weaknesses), &e.Weaknesses)
	if e.Dimensions == nil {
		e.Dimensions = map[string]DimensionScore{}
	}
	if e.Strengths == nil {
		e.Strengths = []string{}
	}
	if e.Weaknesses == nil {
		e.Weaknesses = []string{}
	}
	return e, nil
}

func getEvaluation(q querier, id string) (Evaluation, error) {
	e, err := scanEvaluation(q.QueryRow(`SELECT `+evaluationCols+` FROM evaluations WHERE id = ?`, id))
	return e, notFound(err)
}

func insertEvaluation(q querier, e Evaluation) error {
	_, err := q.Exec(`INSERT INTO evaluations (`+evaluationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.EssayID, e.VersionID, e.Score, e.MaxScore, e.Band, toJSON(e.Dimensions), e.OverallComment,
		toJSON(e.Strengths), toJSON(e.Weaknesses), e.Polished, e.Author, e.CreatedAt)
	return err
}

func listEvaluations(q querier, essayID string) ([]Evaluation, error) {
	rows, err := q.Query(`SELECT `+evaluationCols+` FROM evaluations WHERE essay_id = ? ORDER BY created_at DESC, rowid DESC`, essayID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evaluation{}
	for rows.Next() {
		e, err := scanEvaluation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- annotations ----------

const annotationCols = `id, essay_id, evaluation_id, parent_id, start_pos, end_pos, quote, prefix, suffix, kind, label, note, suggestion, author, status, created_at, updated_at`

func scanAnnotation(r scanner) (Annotation, error) {
	var a Annotation
	var eid, pid, sug sql.NullString
	err := r.Scan(&a.ID, &a.EssayID, &eid, &pid, &a.Start, &a.End, &a.Quote, &a.Prefix, &a.Suffix,
		&a.Kind, &a.Label, &a.Note, &sug, &a.Author, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	a.EvaluationID, a.ParentID, a.Suggestion = strPtr(eid), strPtr(pid), strPtr(sug)
	return a, err
}

func getAnnotation(q querier, id string) (Annotation, error) {
	a, err := scanAnnotation(q.QueryRow(`SELECT `+annotationCols+` FROM annotations WHERE id = ?`, id))
	return a, notFound(err)
}

func insertAnnotation(q querier, a Annotation) error {
	_, err := q.Exec(`INSERT INTO annotations (`+annotationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.EssayID, nullStr(a.EvaluationID), nullStr(a.ParentID), a.Start, a.End, a.Quote, a.Prefix, a.Suffix,
		a.Kind, a.Label, a.Note, nullStr(a.Suggestion), a.Author, a.Status, a.CreatedAt, a.UpdatedAt)
	return err
}

func saveAnnotation(q querier, a Annotation) error {
	_, err := q.Exec(`UPDATE annotations SET start_pos=?, end_pos=?, quote=?, prefix=?, suffix=?, kind=?, label=?,
		note=?, suggestion=?, status=?, updated_at=? WHERE id=?`,
		a.Start, a.End, a.Quote, a.Prefix, a.Suffix, a.Kind, a.Label, a.Note, nullStr(a.Suggestion),
		a.Status, a.UpdatedAt, a.ID)
	return err
}

// listAnnotations 按 Start 升序；回复排在父标注之后（同一 Start 下按创建时间）。
func listAnnotations(q querier, essayID string) ([]Annotation, error) {
	rows, err := q.Query(`SELECT `+annotationCols+` FROM annotations WHERE essay_id = ?
		ORDER BY start_pos, created_at, rowid`, essayID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
