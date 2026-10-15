package core

// 实体与输入类型：REST、MCP、前端共用同一套 JSON 形状（camelCase）。
// 契约说明见 docs/api.md。

// ---------- 实体 ----------

type Essay struct {
	ID          string   `json:"id"`
	Subject     Subject  `json:"subject"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Prompt      string   `json:"prompt"`
	PromptImage string   `json:"promptImage,omitempty"` // data URL，空串表示无图
	Content     string   `json:"content"`
	WordCount   int      `json:"wordCount"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// EssaySummary 是列表项：不含正文、题目与图片。
type EssaySummary struct {
	ID              string        `json:"id"`
	Subject         Subject       `json:"subject"`
	Type            string        `json:"type"`
	Title           string        `json:"title"`
	Category        string        `json:"category"`
	Tags            []string      `json:"tags"`
	WordCount       int           `json:"wordCount"`
	LatestScore     *ScoreSummary `json:"latestScore"`
	AnnotationCount int           `json:"annotationCount"`
	VersionCount    int           `json:"versionCount"`
	UpdatedAt       string        `json:"updatedAt"`
}

type ScoreSummary struct {
	Score     float64 `json:"score"`
	MaxScore  float64 `json:"maxScore"`
	Band      string  `json:"band"`
	CreatedAt string  `json:"createdAt"`
}

// Version 是作答快照。评分时自动生成（EvaluationID 非空），也可手动保存。
type Version struct {
	ID           string   `json:"id"`
	EssayID      string   `json:"essayId"`
	Note         string   `json:"note"`
	Title        string   `json:"title"`
	Type         string   `json:"type"`
	Prompt       string   `json:"prompt"`
	Content      string   `json:"content"`
	WordCount    int      `json:"wordCount"`
	EvaluationID *string  `json:"evaluationId"`
	Score        *float64 `json:"score"`
	Author       string   `json:"author"`
	CreatedAt    string   `json:"createdAt"`
}

type DimensionScore struct {
	Score    float64 `json:"score"`
	MaxScore float64 `json:"maxScore"`
	Feedback string  `json:"feedback"`
}

type Evaluation struct {
	ID             string                    `json:"id"`
	EssayID        string                    `json:"essayId"`
	VersionID      string                    `json:"versionId"`
	Score          float64                   `json:"score"`
	MaxScore       float64                   `json:"maxScore"`
	Band           string                    `json:"band"`
	Dimensions     map[string]DimensionScore `json:"dimensions"`
	OverallComment string                    `json:"overallComment"`
	Strengths      []string                  `json:"strengths"`
	Weaknesses     []string                  `json:"weaknesses"`
	Polished       string                    `json:"polished"`
	Author         string                    `json:"author"`
	CreatedAt      string                    `json:"createdAt"`
}

// Annotation 是挂在正文上的标记 + 边注。
// Start/End 为 UTF-16 码元偏移（与浏览器 textarea.selectionStart 一致），左闭右开。
// ParentID 非空表示这是对另一条标注的回复（如 Agent 回答用户的疑问），与父标注共享区间。
type Annotation struct {
	ID           string  `json:"id"`
	EssayID      string  `json:"essayId"`
	EvaluationID *string `json:"evaluationId"`
	ParentID     *string `json:"parentId"`
	Start        int     `json:"start"`
	End          int     `json:"end"`
	Quote        string  `json:"quote"`
	Prefix       string  `json:"prefix"`
	Suffix       string  `json:"suffix"`
	Kind         string  `json:"kind"`  // AnnotationKinds
	Label        string  `json:"label"` // 细分类，如纠错的“语法语病”；可空
	Note         string  `json:"note"`
	Suggestion   *string `json:"suggestion"` // 替换文本；仅 correction 必填
	Author       string  `json:"author"`     // "user" 或 "agent:<name>"
	Status       string  `json:"status"`     // open / resolved / orphaned
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type EssayDetail struct {
	Essay       Essay        `json:"essay"`
	Annotations []Annotation `json:"annotations"` // 按 Start 升序
	Evaluations []Evaluation `json:"evaluations"` // 新的在前
	Versions    []Version    `json:"versions"`    // 新的在前
}

// Activity 是 Agent（MCP / WebMCP）调用记录，供界面底部“Agent 动态”显示。仅内存保留最近 200 条。
type Activity struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Actor   string `json:"actor"`
	Tool    string `json:"tool"`
	EssayID string `json:"essayId,omitempty"`
	Summary string `json:"summary"`
}

// UIContext 是界面上报的当前视图，Agent 用 get_ui_context 读取。
type UIContext struct {
	EssayID        string `json:"essayId"`
	SelectionStart int    `json:"selectionStart"`
	SelectionEnd   int    `json:"selectionEnd"`
	SelectionText  string `json:"selectionText"`
	UpdatedAt      string `json:"updatedAt"`
}

// Event 通过 SSE 推给界面。Type：essay / annotations / evaluations / versions / deleted / activity / ui-context
type Event struct {
	Type     string    `json:"type"`
	EssayID  string    `json:"essayId,omitempty"`
	Actor    string    `json:"actor,omitempty"`
	Activity *Activity `json:"activity,omitempty"`
}

// ---------- 输入 ----------

type EssayFilter struct {
	Subject string `json:"subject,omitempty"`
	Type    string `json:"type,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Query   string `json:"query,omitempty"` // 标题/题目/正文子串
}

type CreateEssayInput struct {
	Subject     Subject  `json:"subject"`
	Type        string   `json:"type"`
	Title       string   `json:"title,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags"`
	Prompt      string   `json:"prompt,omitempty"`
	PromptImage string   `json:"promptImage,omitempty"`
	Content     string   `json:"content,omitempty"`
}

// UpdateEssayInput：nil 字段不改。PromptImage 传空串表示删除图片。Tags 整体替换且不能为空。
type UpdateEssayInput struct {
	Type        *string   `json:"type,omitempty"`
	Title       *string   `json:"title,omitempty"`
	Category    *string   `json:"category,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Prompt      *string   `json:"prompt,omitempty"`
	PromptImage *string   `json:"promptImage,omitempty"`
	Content     *string   `json:"content,omitempty"`
}

// AnnotateItem：给 Quote（Agent 常用，服务端定位）或 Start/End（界面选区）其一。
// Occurrence 从 1 开始，Quote 多次出现时指定第几处；0 视为 1。
// ParentID 非空时为回复，区间取父标注的区间，Quote/Start/End 可省略。
type AnnotateItem struct {
	Quote      string  `json:"quote,omitempty"`
	Occurrence int     `json:"occurrence,omitempty"`
	Start      *int    `json:"start,omitempty"`
	End        *int    `json:"end,omitempty"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label,omitempty"`
	Note       string  `json:"note,omitempty"`
	Suggestion *string `json:"suggestion,omitempty"`
	ParentID   *string `json:"parentId,omitempty"`
}

type AnnotateResult struct {
	Index      int         `json:"index"`
	Annotation *Annotation `json:"annotation,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// HighlightItem 是跨作文的重点句：一条 kind=highlight 的顶层标注，附带所在作文的归类信息。
type HighlightItem struct {
	Annotation
	EssayTitle    string   `json:"essayTitle"`
	EssaySubject  Subject  `json:"essaySubject"`
	EssayType     string   `json:"essayType"`
	EssayCategory string   `json:"essayCategory"`
	EssayTags     []string `json:"essayTags"`
}

type AnnotationFilter struct {
	Author string `json:"author,omitempty"` // "user" / "agent"（前缀匹配 agent:*）/ 完整作者名
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
}

// AnnotationPatch：nil 不改。给 Start/End 表示重新定位（重算 quote/prefix/suffix，状态回到 open）。
type AnnotationPatch struct {
	Kind       *string `json:"kind,omitempty"`
	Label      *string `json:"label,omitempty"`
	Note       *string `json:"note,omitempty"`
	Suggestion *string `json:"suggestion,omitempty"`
	Status     *string `json:"status,omitempty"`
	Start      *int    `json:"start,omitempty"`
	End        *int    `json:"end,omitempty"`
}

type DimensionInput struct {
	Score    float64 `json:"score"`
	Feedback string  `json:"feedback"`
}

type CorrectionInput struct {
	Quote       string `json:"quote"`
	Occurrence  int    `json:"occurrence,omitempty"`
	Corrected   string `json:"corrected"`
	Kind        string `json:"kind"` // CorrectionKinds[subject]
	Explanation string `json:"explanation"`
}

type EvaluationInput struct {
	Score          float64                   `json:"score"`
	Band           string                    `json:"band"`
	Dimensions     map[string]DimensionInput `json:"dimensions"`
	OverallComment string                    `json:"overallComment"`
	Strengths      []string                  `json:"strengths"`
	Weaknesses     []string                  `json:"weaknesses"`
	Polished       string                    `json:"polished"`
	Corrections    []CorrectionInput         `json:"corrections"`
}

type EvaluationResult struct {
	Evaluation  Evaluation       `json:"evaluation"`
	Version     Version          `json:"version"`
	Corrections []AnnotateResult `json:"corrections"`
}
