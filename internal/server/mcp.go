package server

// MCP 服务：本地 Agent 通过这些工具读写作文、标注与评分。
// 服务端不调用模型；评分、润色都由 Agent 完成后写回。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wesleyel/EssayPilot/internal/core"
)

const mcpInstructions = `EssayPilot 是考研作文（英语 Part A/B、政治主观题）批阅工作台。服务端不调用任何模型：评分、纠错、润色都由你（Agent）完成，再通过这些工具写回，用户会在网页界面上实时看到你的改动。

- 用户说“这篇”“这一句”时，先调用 get_ui_context，看用户当前打开的是哪篇、选中了什么文字。
- 评分流程：get_rubric → get_essay → submit_evaluation（一次提交总分、档次、四维分、总评、亮点短板、润色稿与纠错项）。四维之和必须等于总分，每维不超过自己的满分。
- 标注（annotate）与纠错（corrections）的 quote 必须逐字摘自当前正文（包括标点），尽量短而唯一；同一片段多次出现时用 occurrence 指明第几处。纠错类标注要带 suggestion，用户可一键采纳。
- ` + core.HighlightRule + `也可以用 prompt mark_highlights。
- 其他边注（亮点、问题、疑问、纠错）只写一句，点明作用或错在哪，不复述原文。
- 用户自己做的标注（author = user）是他们的疑问或关注点：用 reply 回答，不要删除或改写用户的标注。
- 不确定题型规格、分类或维度时调用 list_types。`

// mcpCall 记录一次工具调用的上下文，处理函数填写 essayID 与摘要，由外层统一写 Activity。
type mcpCall struct {
	actor   string
	essayID string
	summary string
}

func (c *mcpCall) note(essayID, format string, args ...any) {
	if essayID != "" {
		c.essayID = essayID
	}
	c.summary = fmt.Sprintf(format, args...)
}

// clientActor 返回 "agent:<客户端名>"。新协议（≥ 2026-07-28）下客户端信息随请求的 _meta 传来；
// 旧协议从会话的 InitializeParams 取；stateless 旧协议拿不到时回退 "agent:mcp"。
func clientActor(info *mcp.Implementation) string {
	if info == nil {
		return "agent:mcp"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(info.Name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '/' || r == ':':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "agent:mcp"
	}
	return "agent:" + name
}

// addTool 包一层：取 actor、调用处理函数、统一写 Activity 日志。
func addTool[In, Out any](srv *mcp.Server, s *core.Service, t *mcp.Tool, fn func(c *mcpCall, in In) (Out, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		c := &mcpCall{actor: clientActor(req.ClientInfo())}
		out, err := fn(c, in)
		if err != nil {
			s.Log(c.actor, t.Name, c.essayID, "失败："+err.Error())
			var zero Out
			return nil, zero, toolError(err)
		}
		if c.summary == "" {
			c.summary = "完成"
		}
		s.Log(c.actor, t.Name, c.essayID, c.summary)
		return nil, out, nil
	})
}

func toolError(err error) error {
	if errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("未找到：%v", err)
	}
	return err
}

func ptr[T any](v T) *T { return &v }

var (
	readOnly    = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	writeTool   = &mcp.ToolAnnotations{DestructiveHint: ptr(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true}
)

// ---------- 输入输出类型 ----------

type listEssaysIn struct {
	Subject string `json:"subject,omitempty" jsonschema:"科目：english 或 politics"`
	Type    string `json:"type,omitempty" jsonschema:"题型 id，如 part-a、part-b、mayuan（见 list_types）"`
	Tag     string `json:"tag,omitempty" jsonschema:"主题标签"`
	Query   string `json:"query,omitempty" jsonschema:"在标题、题目、正文中搜索的子串"`
}

type listEssaysOut struct {
	Essays []core.EssaySummary `json:"essays"`
}

type idIn struct {
	ID string `json:"id" jsonschema:"作文（essay）id"`
}

type essayIDIn struct {
	EssayID string `json:"essay_id" jsonschema:"作文（essay）id"`
}

// essayView 是 EssayDetail 加上按段编号的正文。
type essayView struct {
	Essay       core.Essay        `json:"essay"`
	Annotations []core.Annotation `json:"annotations" jsonschema:"标注，按位置升序；author 为 user 的是用户自己的标记"`
	Evaluations []core.Evaluation `json:"evaluations" jsonschema:"评分记录，新的在前"`
	Versions    []core.Version    `json:"versions" jsonschema:"版本快照，新的在前"`
	Numbered    string            `json:"numbered" jsonschema:"正文按段编号的文本（¶1 ¶2 …），方便引用；quote 仍须摘自原文、不含编号"`
}

func viewOf(d core.EssayDetail) essayView {
	return essayView{d.Essay, d.Annotations, d.Evaluations, d.Versions, numberedText(d.Essay.Content)}
}

func numberedText(content string) string {
	var b strings.Builder
	n := 0
	for line := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "¶%d %s\n", n, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

type createEssayIn struct {
	Subject  string   `json:"subject" jsonschema:"科目：english 或 politics"`
	Type     string   `json:"type" jsonschema:"题型 id，须属于该科目（见 list_types）"`
	Title    string   `json:"title,omitempty" jsonschema:"标题，留空为“未命名”"`
	Category string   `json:"category,omitempty" jsonschema:"分类：英语题型必填且须取该题型的分类之一；政治题型留空"`
	Tags     []string `json:"tags" jsonschema:"主题标签，至少一个，最多 10 个，每个不超过 20 字"`
	Prompt   string   `json:"prompt,omitempty" jsonschema:"题目要求原文"`
	Content  string   `json:"content,omitempty" jsonschema:"作答正文"`
}

type updateEssayIn struct {
	ID       string    `json:"id" jsonschema:"作文 id"`
	Type     *string   `json:"type,omitempty" jsonschema:"新题型 id，只能在本科目内切换"`
	Title    *string   `json:"title,omitempty" jsonschema:"新标题"`
	Category *string   `json:"category,omitempty" jsonschema:"新分类，须与题型匹配"`
	Tags     *[]string `json:"tags,omitempty" jsonschema:"新的主题标签（整体替换，不能为空）"`
	Prompt   *string   `json:"prompt,omitempty" jsonschema:"新的题目要求"`
	Content  *string   `json:"content,omitempty" jsonschema:"新的正文（整体替换；已有标注会自动重新锚定，找不到原文的变为 orphaned）"`
}

type okOut struct {
	OK bool `json:"ok"`
}

type saveVersionIn struct {
	EssayID string `json:"essay_id" jsonschema:"作文 id"`
	Note    string `json:"note,omitempty" jsonschema:"版本备注，如“润色前”"`
}

type listVersionsOut struct {
	Versions []core.Version `json:"versions"`
}

type restoreVersionIn struct {
	EssayID   string `json:"essay_id" jsonschema:"作文 id"`
	VersionID string `json:"version_id" jsonschema:"要恢复的版本 id（见 list_versions）"`
}

type annotateItemIn struct {
	Quote      string  `json:"quote" jsonschema:"逐字摘自当前正文的片段（含标点），尽量短而唯一"`
	Occurrence int     `json:"occurrence,omitempty" jsonschema:"quote 在正文中多次出现时指定第几处，从 1 开始，默认 1"`
	Kind       string  `json:"kind" jsonschema:"highlight 重点 / good 亮点 / issue 问题 / question 疑问 / correction 纠错"`
	Label      string  `json:"label,omitempty" jsonschema:"细分类：纠错类别（英语 spelling/grammar/collocation/polish，政治 concept/analysis/discourse/structure/grammar）；重点写使用场景（2–6 字），如“开头表明来意”“转另一个话题”“结尾祝愿”"`
	Note       string  `json:"note,omitempty" jsonschema:"边注，一句话点明作用或错在哪，不解释字面意思、不复述原文；重点留空（场景写在 label）；可写简单 markdown"`
	Suggestion *string `json:"suggestion,omitempty" jsonschema:"替换文本；kind 为 correction 时必填，用户可一键采纳"`
}

type annotateIn struct {
	EssayID string           `json:"essay_id" jsonschema:"作文 id"`
	Items   []annotateItemIn `json:"items" jsonschema:"要添加的标注，可批量"`
}

type annotateOut struct {
	Results []core.AnnotateResult `json:"results" jsonschema:"与 items 一一对应；error 非空的条目未保存（多为未找到原文），修正 quote 后重试"`
	Added   int                   `json:"added"`
	Failed  int                   `json:"failed"`
}

type replyIn struct {
	AnnotationID string `json:"annotation_id" jsonschema:"要回复的标注 id（通常是 author 为 user 的标注）"`
	Note         string `json:"note" jsonschema:"回复内容，可写简单 markdown"`
}

type listAnnotationsIn struct {
	EssayID string `json:"essay_id" jsonschema:"作文 id"`
	Author  string `json:"author,omitempty" jsonschema:"user（用户自己的标记）/ agent（所有 Agent）/ 完整作者名"`
	Kind    string `json:"kind,omitempty" jsonschema:"highlight / good / issue / question / correction"`
	Status  string `json:"status,omitempty" jsonschema:"open / resolved / orphaned"`
}

type listAnnotationsOut struct {
	Annotations []core.Annotation `json:"annotations"`
}

type updateAnnotationIn struct {
	ID         string  `json:"id" jsonschema:"标注 id"`
	Kind       *string `json:"kind,omitempty" jsonschema:"新类型"`
	Label      *string `json:"label,omitempty" jsonschema:"新细分类"`
	Note       *string `json:"note,omitempty" jsonschema:"新边注正文"`
	Suggestion *string `json:"suggestion,omitempty" jsonschema:"新替换文本"`
	Status     *string `json:"status,omitempty" jsonschema:"open / resolved"`
}

type annotationIDIn struct {
	AnnotationID string `json:"annotation_id" jsonschema:"标注 id"`
}

type applySuggestionsIn struct {
	EssayID string   `json:"essay_id" jsonschema:"作文 id"`
	IDs     []string `json:"ids,omitempty" jsonschema:"要采纳的纠错标注 id；省略则采纳全部 open 且带 suggestion 的标注"`
}

type getRubricIn struct {
	Type    string `json:"type,omitempty" jsonschema:"题型 id（与 essay_id 二选一）"`
	EssayID string `json:"essay_id,omitempty" jsonschema:"作文 id，按其题型返回细则"`
}

type dimensionOut struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	MaxScore float64 `json:"maxScore"`
	Criteria string  `json:"criteria"`
}

type rubricOut struct {
	Spec            core.TypeSpec  `json:"spec"`
	Dimensions      []dimensionOut `json:"dimensions" jsonschema:"四个评分维度及各自满分"`
	CorrectionKinds []string       `json:"correctionKinds" jsonschema:"corrections[].kind 可取的值"`
	Text            string         `json:"text" jsonschema:"完整阅卷细则"`
}

type dimensionScoreIn struct {
	Score    float64 `json:"score" jsonschema:"该维度得分，不超过该维度满分"`
	Feedback string  `json:"feedback" jsonschema:"该维度的具体评语"`
}

type dimensionsIn struct {
	Content    dimensionScoreIn `json:"content"`
	Language   dimensionScoreIn `json:"language"`
	Structure  dimensionScoreIn `json:"structure"`
	Convention dimensionScoreIn `json:"convention"`
}

type correctionIn struct {
	Quote       string `json:"quote" jsonschema:"逐字摘自正文的原文片段"`
	Occurrence  int    `json:"occurrence,omitempty" jsonschema:"quote 多次出现时指定第几处，从 1 开始"`
	Corrected   string `json:"corrected" jsonschema:"修改后的文本"`
	Kind        string `json:"kind" jsonschema:"纠错类别，见 get_rubric 的 correctionKinds"`
	Explanation string `json:"explanation" jsonschema:"一句话：错在哪、会因此丢什么分；不复述原文"`
}

type submitEvaluationIn struct {
	EssayID        string         `json:"essay_id" jsonschema:"作文 id"`
	Score          float64        `json:"score" jsonschema:"总分，等于四维之和，不超过题型满分"`
	Band           string         `json:"band" jsonschema:"档次，如 第四档 / 高分档"`
	Dimensions     dimensionsIn   `json:"dimensions" jsonschema:"四维得分与评语"`
	OverallComment string         `json:"overall_comment" jsonschema:"约 200 字考官总评"`
	Strengths      []string       `json:"strengths" jsonschema:"亮点条目"`
	Weaknesses     []string       `json:"weaknesses" jsonschema:"短板条目"`
	Polished       string         `json:"polished" jsonschema:"整篇修订稿（最小必要修改）"`
	Corrections    []correctionIn `json:"corrections,omitempty" jsonschema:"纠错项，会作为纠错边注挂到原文上"`
}

type deleteEvaluationIn struct {
	EssayID      string `json:"essay_id" jsonschema:"作文 id"`
	EvaluationID string `json:"evaluation_id" jsonschema:"评分 id"`
}

type uiContextOut struct {
	core.UIContext
	Title string `json:"title,omitempty" jsonschema:"当前打开作文的标题"`
	Hint  string `json:"hint" jsonschema:"说明"`
}

type listTypesOut struct {
	Types                []core.TypeSpec                                `json:"types"`
	Dimensions           map[core.Subject]map[string]core.DimensionSpec `json:"dimensions" jsonschema:"各科目四维的名称、权重（乘以题型满分即维度满分）与标准"`
	DimensionKeys        []string                                       `json:"dimensionKeys"`
	CorrectionKinds      map[core.Subject][]string                      `json:"correctionKinds"`
	CorrectionLabels     map[string]string                              `json:"correctionLabels"`
	AnnotationKinds      []string                                       `json:"annotationKinds"`
	AnnotationKindLabels map[string]string                              `json:"annotationKindLabels"`
}

// ---------- 服务 ----------

func newMCPServer(s *core.Service) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "essaypilot", Title: "EssayPilot", Version: "2.0.0"},
		&mcp.ServerOptions{Instructions: mcpInstructions})

	addTool(srv, s, &mcp.Tool{Name: "list_essays", Annotations: readOnly,
		Description: "列出作文（不含正文），可按科目、题型、标签筛选或搜索。用户提到某篇但没给 id 时先用它查找。"},
		func(c *mcpCall, in listEssaysIn) (listEssaysOut, error) {
			list, err := s.ListEssays(core.EssayFilter{Subject: in.Subject, Type: in.Type, Tag: in.Tag, Query: in.Query})
			if err != nil {
				return listEssaysOut{}, err
			}
			c.note("", "列出 %d 篇", len(list))
			return listEssaysOut{Essays: list}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "get_essay", Annotations: readOnly,
		Description: "读取一篇作文的全部信息：题目、正文、全部标注、评分记录、版本，以及按段编号的正文（numbered）。评分、标注前先读最新正文。"},
		func(c *mcpCall, in idIn) (essayView, error) {
			d, err := s.GetEssay(in.ID)
			if err != nil {
				return essayView{}, err
			}
			c.note(in.ID, "读取《%s》", d.Essay.Title)
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "create_essay", Annotations: writeTool,
		Description: "新建一篇作文（题目 + 作答）。英语题型须选分类，至少一个主题标签。"},
		func(c *mcpCall, in createEssayIn) (essayView, error) {
			d, err := s.CreateEssay(c.actor, core.CreateEssayInput{Subject: core.Subject(in.Subject), Type: in.Type, Title: in.Title,
				Category: in.Category, Tags: in.Tags, Prompt: in.Prompt, Content: in.Content})
			if err != nil {
				return essayView{}, err
			}
			c.note(d.Essay.ID, "新建《%s》", d.Essay.Title)
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "update_essay", Annotations: writeTool,
		Description: "修改作文的标题、题型、分类、标签、题目或正文，只传要改的字段。改正文前建议先 save_version；只想提修改建议时用 annotate 的 correction，不要直接改正文。"},
		func(c *mcpCall, in updateEssayIn) (essayView, error) {
			d, err := s.UpdateEssay(c.actor, in.ID, core.UpdateEssayInput{Type: in.Type, Title: in.Title, Category: in.Category,
				Tags: in.Tags, Prompt: in.Prompt, Content: in.Content})
			if err != nil {
				return essayView{}, err
			}
			fields := []string{}
			for name, set := range map[string]bool{"题型": in.Type != nil, "标题": in.Title != nil, "分类": in.Category != nil,
				"标签": in.Tags != nil, "题目": in.Prompt != nil, "正文": in.Content != nil} {
				if set {
					fields = append(fields, name)
				}
			}
			c.note(in.ID, "修改 %d 个字段", len(fields))
			if in.Content != nil {
				c.summary = fmt.Sprintf("修改正文（%d %s）", d.Essay.WordCount, core.LengthUnit(d.Essay.Subject))
			}
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "delete_essay", Annotations: destructive,
		Description: "永久删除一篇作文及其全部版本、评分、标注。仅在用户明确要求时使用。"},
		func(c *mcpCall, in idIn) (okOut, error) {
			if err := s.DeleteEssay(c.actor, in.ID); err != nil {
				return okOut{}, err
			}
			c.note(in.ID, "删除作文")
			return okOut{true}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "save_version", Annotations: writeTool,
		Description: "把当前稿保存为版本快照（评分时会自动生成，无需手动）。大改正文前用它留底。"},
		func(c *mcpCall, in saveVersionIn) (core.Version, error) {
			v, err := s.SaveVersion(c.actor, in.EssayID, in.Note)
			if err != nil {
				return core.Version{}, err
			}
			c.note(in.EssayID, "保存版本%s", quoteNote(in.Note))
			return v, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "list_versions", Annotations: readOnly,
		Description: "列出作文的历史版本（含每版正文与对应评分），新的在前。"},
		func(c *mcpCall, in essayIDIn) (listVersionsOut, error) {
			d, err := s.GetEssay(in.EssayID)
			if err != nil {
				return listVersionsOut{}, err
			}
			c.note(in.EssayID, "%d 个版本", len(d.Versions))
			return listVersionsOut{Versions: d.Versions}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "restore_version", Annotations: writeTool,
		Description: "把作文恢复到某个历史版本（当前稿会先自动存为“恢复前自动保存”版本）。"},
		func(c *mcpCall, in restoreVersionIn) (essayView, error) {
			d, err := s.RestoreVersion(c.actor, in.EssayID, in.VersionID)
			if err != nil {
				return essayView{}, err
			}
			c.note(in.EssayID, "恢复到版本 %s", in.VersionID)
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "annotate", Annotations: writeTool,
		Description: "在正文上批量添加标注（重点 / 亮点 / 问题 / 疑问 / 纠错），显示为页边边注。重点只画可复用的开头句、结尾句、承接过渡词句和万能句型，label 写使用场景。quote 必须逐字摘自当前正文；纠错要带 suggestion。返回每条结果，未找到原文的条目会带 error。"},
		func(c *mcpCall, in annotateIn) (annotateOut, error) {
			items := make([]core.AnnotateItem, len(in.Items))
			for i, it := range in.Items {
				items[i] = core.AnnotateItem{Quote: it.Quote, Occurrence: it.Occurrence, Kind: it.Kind, Label: it.Label,
					Note: it.Note, Suggestion: it.Suggestion}
			}
			res, err := s.Annotate(c.actor, in.EssayID, items)
			if err != nil {
				return annotateOut{}, err
			}
			out := annotateOut{Results: res}
			for _, r := range res {
				if r.Error == "" {
					out.Added++
				} else {
					out.Failed++
				}
			}
			if out.Failed > 0 {
				c.note(in.EssayID, "新增 %d 条标注，%d 条未找到原文", out.Added, out.Failed)
			} else {
				c.note(in.EssayID, "新增 %d 条标注", out.Added)
			}
			return out, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "reply", Annotations: writeTool,
		Description: "回复一条标注（通常是回答用户 author=user 的疑问），回复显示在该边注下方，与原标注共享位置。"},
		func(c *mcpCall, in replyIn) (core.Annotation, error) {
			parentAnn, err := s.GetAnnotation(in.AnnotationID)
			if err != nil {
				return core.Annotation{}, err
			}
			essayID := parentAnn.EssayID
			parent := in.AnnotationID
			res, err := s.Annotate(c.actor, essayID, []core.AnnotateItem{{Note: in.Note, ParentID: &parent}})
			c.essayID = essayID
			if err != nil {
				return core.Annotation{}, err
			}
			if len(res) == 0 || res[0].Annotation == nil {
				msg := "回复失败"
				if len(res) > 0 && res[0].Error != "" {
					msg = res[0].Error
				}
				return core.Annotation{}, &core.ValidationError{Msg: msg}
			}
			c.note(essayID, "回复标注：%s", clip(in.Note, 24))
			return *res[0].Annotation, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "list_annotations", Annotations: readOnly,
		Description: "列出作文上的标注，可按作者（user 为用户自己的标记）、类型、状态筛选。回应用户标记前先用 author=user 调用。"},
		func(c *mcpCall, in listAnnotationsIn) (listAnnotationsOut, error) {
			list, err := s.ListAnnotations(in.EssayID, core.AnnotationFilter{Author: in.Author, Kind: in.Kind, Status: in.Status})
			if err != nil {
				return listAnnotationsOut{}, err
			}
			c.note(in.EssayID, "%d 条标注", len(list))
			return listAnnotationsOut{Annotations: list}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "update_annotation", Annotations: writeTool,
		Description: "修改一条标注的类型、细分类、边注、替换建议或状态（resolved 表示已处理）。不要改用户自己的标注内容。"},
		func(c *mcpCall, in updateAnnotationIn) (core.Annotation, error) {
			a, err := s.UpdateAnnotation(c.actor, in.ID, core.AnnotationPatch{Kind: in.Kind, Label: in.Label, Note: in.Note,
				Suggestion: in.Suggestion, Status: in.Status})
			if err != nil {
				return core.Annotation{}, err
			}
			c.note(a.EssayID, "修改标注「%s」", clip(a.Quote, 20))
			return a, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "delete_annotation", Annotations: destructive,
		Description: "删除一条标注（连同其回复）。不要删除用户自己的标注，除非用户要求。"},
		func(c *mcpCall, in idIn) (okOut, error) {
			if a, err := s.GetAnnotation(in.ID); err == nil {
				c.essayID = a.EssayID
			}
			if err := s.DeleteAnnotation(c.actor, in.ID); err != nil {
				return okOut{}, err
			}
			c.note("", "删除标注")
			return okOut{true}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "apply_suggestion", Annotations: writeTool,
		Description: "采纳一条纠错标注：用它的 suggestion 替换正文中对应片段，该标注标为 resolved，其余标注自动重新锚定。"},
		func(c *mcpCall, in annotationIDIn) (essayView, error) {
			d, err := s.ApplySuggestion(c.actor, in.AnnotationID)
			if err != nil {
				return essayView{}, err
			}
			c.note(d.Essay.ID, "采纳 1 条修改")
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "apply_suggestions", Annotations: writeTool,
		Description: "批量采纳纠错标注（省略 ids 则采纳全部 open 且带 suggestion 的标注）。仅在用户要求“全部采纳”时使用。"},
		func(c *mcpCall, in applySuggestionsIn) (essayView, error) {
			before := 0
			if in.IDs == nil {
				if list, err := s.ListAnnotations(in.EssayID, core.AnnotationFilter{Status: core.StatusOpen}); err == nil {
					for _, a := range list {
						if a.Suggestion != nil && a.ParentID == nil {
							before++
						}
					}
				}
			} else {
				before = len(in.IDs)
			}
			d, err := s.ApplySuggestions(c.actor, in.EssayID, in.IDs)
			if err != nil {
				return essayView{}, err
			}
			c.note(in.EssayID, "采纳 %d 条修改", before)
			return viewOf(d), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "get_rubric", Annotations: readOnly,
		Description: "获取题型的阅卷细则：满分、四维满分与标准、字数要求、纠错类别与提交要求。评分前必读。传 type 或 essay_id。"},
		func(c *mcpCall, in getRubricIn) (rubricOut, error) {
			typ := in.Type
			if typ == "" && in.EssayID != "" {
				d, err := s.GetEssay(in.EssayID)
				if err != nil {
					return rubricOut{}, err
				}
				typ = d.Essay.Type
			}
			spec, ok := core.LookupType(typ)
			if !ok {
				return rubricOut{}, &core.ValidationError{Msg: fmt.Sprintf("未知题型：%q（传 type 或 essay_id，题型见 list_types）", typ)}
			}
			c.note(in.EssayID, "%s 细则", spec.Label)
			return rubricOf(spec), nil
		})

	addTool(srv, s, &mcp.Tool{Name: "submit_evaluation", Annotations: writeTool,
		Description: "提交评分结果：总分、档次、四维分与评语、总评、亮点短板、润色稿、纠错项。服务端校验后自动生成版本快照，纠错项转成可采纳的纠错边注。被拒时按错误信息修正后重交。"},
		func(c *mcpCall, in submitEvaluationIn) (core.EvaluationResult, error) {
			corr := make([]core.CorrectionInput, len(in.Corrections))
			for i, x := range in.Corrections {
				corr[i] = core.CorrectionInput{Quote: x.Quote, Occurrence: x.Occurrence, Corrected: x.Corrected, Kind: x.Kind, Explanation: x.Explanation}
			}
			dim := func(d dimensionScoreIn) core.DimensionInput {
				return core.DimensionInput{Score: d.Score, Feedback: d.Feedback}
			}
			c.essayID = in.EssayID
			r, err := s.SubmitEvaluation(c.actor, in.EssayID, core.EvaluationInput{
				Score: in.Score, Band: in.Band,
				Dimensions: map[string]core.DimensionInput{"content": dim(in.Dimensions.Content), "language": dim(in.Dimensions.Language),
					"structure": dim(in.Dimensions.Structure), "convention": dim(in.Dimensions.Convention)},
				OverallComment: in.OverallComment, Strengths: in.Strengths, Weaknesses: in.Weaknesses,
				Polished: in.Polished, Corrections: corr,
			})
			if err != nil {
				return core.EvaluationResult{}, err
			}
			failed := 0
			for _, x := range r.Corrections {
				if x.Error != "" {
					failed++
				}
			}
			c.note(in.EssayID, "%g/%g · %s · 生成版本", r.Evaluation.Score, r.Evaluation.MaxScore, r.Evaluation.Band)
			if failed > 0 {
				c.summary += fmt.Sprintf("，%d 条纠错未找到原文", failed)
			}
			return r, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "delete_evaluation", Annotations: destructive,
		Description: "删除一次评分记录。仅在用户要求或评分明显提交错误时使用。"},
		func(c *mcpCall, in deleteEvaluationIn) (okOut, error) {
			if err := s.DeleteEvaluation(c.actor, in.EssayID, in.EvaluationID); err != nil {
				return okOut{}, err
			}
			c.note(in.EssayID, "删除评分")
			return okOut{true}, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "get_ui_context", Annotations: readOnly,
		Description: "获取用户在网页界面里当前打开的作文 id 和选中的文字（UTF-16 偏移与原文）。用户说“这篇”“这一句”“这里”时先调用。"},
		func(c *mcpCall, _ struct{}) (uiContextOut, error) {
			ui := s.UIContext()
			out := uiContextOut{UIContext: ui}
			if ui.EssayID == "" {
				out.Hint = "用户当前没有打开作文"
				c.note("", "未打开作文")
				return out, nil
			}
			if d, err := s.GetEssay(ui.EssayID); err == nil {
				out.Title = d.Essay.Title
			}
			if ui.SelectionText != "" {
				out.Hint = "用户在该作文中选中了 selectionText"
				c.note(ui.EssayID, "选中「%s」", clip(ui.SelectionText, 20))
			} else {
				out.Hint = "用户打开了该作文，没有选中文字"
				c.note(ui.EssayID, "查看《%s》", out.Title)
			}
			return out, nil
		})

	addTool(srv, s, &mcp.Tool{Name: "list_types", Annotations: readOnly,
		Description: "列出全部题型（满分、字数、分类）、各科目评分维度、纠错类别与标注类型。新建作文或不确定取值时调用。"},
		func(c *mcpCall, _ struct{}) (listTypesOut, error) {
			c.note("", "%d 个题型", len(core.Types))
			return listTypesOut{Types: core.Types, Dimensions: core.Dimensions, DimensionKeys: core.DimensionKeys,
				CorrectionKinds: core.CorrectionKinds, CorrectionLabels: core.CorrectionLabels,
				AnnotationKinds: core.AnnotationKinds, AnnotationKindLabels: core.AnnotationKindLabels}, nil
		})

	// ---------- resources ----------

	srv.AddResourceTemplate(&mcp.ResourceTemplate{Name: "essay", Title: "作文全文", URITemplate: "essay://{id}",
		MIMEType: "text/markdown", Description: "一篇作文的 markdown 全文：题目、正文、标注列表、最新评分"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			id := resourceID(req.Params.URI, "essay")
			d, err := s.GetEssay(id)
			if err != nil {
				if errors.Is(err, core.ErrNotFound) {
					return nil, mcp.ResourceNotFoundError(req.Params.URI)
				}
				return nil, err
			}
			return textResource(req.Params.URI, essayMarkdown(d)), nil
		})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{Name: "rubric", Title: "阅卷细则", URITemplate: "rubric://{type}",
		MIMEType: "text/markdown", Description: "某题型的完整阅卷细则，type 为题型 id（如 part-b、mayuan）"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			spec, ok := core.LookupType(resourceID(req.Params.URI, "rubric"))
			if !ok {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return textResource(req.Params.URI, "# "+spec.Label+" 阅卷细则\n\n"+core.Rubric(spec)), nil
		})

	// ---------- prompts ----------

	for _, name := range core.PromptNames {
		srv.AddPrompt(&mcp.Prompt{Name: name, Description: core.PromptDescriptions[name],
			Arguments: []*mcp.PromptArgument{{Name: "essay_id", Description: "作文 id", Required: true}}},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				id := req.Params.Arguments["essay_id"]
				d, err := s.GetEssay(id)
				if err != nil {
					return nil, toolError(err)
				}
				text, err := core.PromptText(name, d)
				if err != nil {
					return nil, err
				}
				return &mcp.GetPromptResult{Description: core.PromptDescriptions[name],
					Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
			})
	}
	return srv
}

// newMCPHandler 返回挂在 /mcp 的 Streamable HTTP handler（无状态）。
func newMCPHandler(s *core.Service) http.Handler {
	srv := newMCPServer(s)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true})
}

// ---------- 辅助 ----------

func rubricOf(spec core.TypeSpec) rubricOut {
	out := rubricOut{Spec: spec, CorrectionKinds: core.CorrectionKinds[spec.Subject], Text: core.Rubric(spec)}
	for _, k := range core.DimensionKeys {
		d := core.Dimensions[spec.Subject][k]
		out.Dimensions = append(out.Dimensions, dimensionOut{k, d.Label, core.DimensionMax(spec.Subject, spec.MaxScore, k), d.Criteria})
	}
	return out
}

func resourceID(uri, scheme string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme == scheme {
		if u.Host != "" {
			return u.Host
		}
		return strings.TrimPrefix(u.Opaque, "//")
	}
	return strings.TrimPrefix(uri, scheme+"://")
}

func textResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: text}}}
}

func essayMarkdown(d core.EssayDetail) string {
	e := d.Essay
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", e.Title)
	label := e.Type
	if spec, ok := core.LookupType(e.Type); ok {
		label = fmt.Sprintf("%s（满分 %g）", spec.Label, spec.MaxScore)
	}
	fmt.Fprintf(&b, "- id：%s\n- 题型：%s\n", e.ID, label)
	if e.Category != "" {
		fmt.Fprintf(&b, "- 分类：%s\n", e.Category)
	}
	fmt.Fprintf(&b, "- 标签：%s\n- 字数：%d\n- 更新：%s\n\n", strings.Join(e.Tags, "、"), e.WordCount, e.UpdatedAt)
	fmt.Fprintf(&b, "## 题目\n\n%s\n\n## 正文\n\n%s\n\n", orNone(e.Prompt), orNone(e.Content))

	b.WriteString("## 标注\n\n")
	if len(d.Annotations) == 0 {
		b.WriteString("（无）\n\n")
	}
	for _, a := range d.Annotations {
		indent := "- "
		if a.ParentID != nil {
			indent = "  - ↳ "
		}
		fmt.Fprintf(&b, "%s[%s", indent, core.AnnotationKindLabels[a.Kind])
		if a.Label != "" {
			fmt.Fprintf(&b, "·%s", a.Label)
		}
		fmt.Fprintf(&b, "] `%s` %s「%s」", a.ID, a.Author, a.Quote)
		if a.Suggestion != nil {
			fmt.Fprintf(&b, " → 「%s」", *a.Suggestion)
		}
		if a.Note != "" {
			fmt.Fprintf(&b, "：%s", a.Note)
		}
		if a.Status != core.StatusOpen {
			fmt.Fprintf(&b, "（%s）", a.Status)
		}
		b.WriteString("\n")
	}
	if len(d.Annotations) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("## 最新评分\n\n")
	if len(d.Evaluations) == 0 {
		b.WriteString("（尚未评分）\n")
		return b.String()
	}
	ev := d.Evaluations[0]
	fmt.Fprintf(&b, "**%g / %g · %s**（%s，%s）\n\n", ev.Score, ev.MaxScore, ev.Band, ev.Author, ev.CreatedAt)
	for _, k := range core.DimensionKeys {
		if ds, ok := ev.Dimensions[k]; ok {
			fmt.Fprintf(&b, "- %s %g/%g：%s\n", core.Dimensions[e.Subject][k].Label, ds.Score, ds.MaxScore, ds.Feedback)
		}
	}
	fmt.Fprintf(&b, "\n**总评**：%s\n", ev.OverallComment)
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n**%s**\n\n", title)
		for _, it := range items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	list("亮点", ev.Strengths)
	list("短板", ev.Weaknesses)
	if ev.Polished != "" {
		fmt.Fprintf(&b, "\n**润色稿**\n\n%s\n", ev.Polished)
	}
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（空）"
	}
	return s
}

func quoteNote(note string) string {
	if note == "" {
		return ""
	}
	return "「" + clip(note, 20) + "」"
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
