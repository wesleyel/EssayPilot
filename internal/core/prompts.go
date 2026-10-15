package core

// MCP prompts 与界面“复制 prompt”按钮共用的提示词。
// 生成的文本要能独立使用：贴进任何接了 essaypilot MCP 的 Agent 都能照做。

import (
	"fmt"
	"strings"
)

// PromptNames 是支持的提示词名称，顺序即展示顺序。
var PromptNames = []string{"grade_essay", "polish_essay", "review_my_marks"}

var PromptDescriptions = map[string]string{
	"grade_essay":     "按考研阅卷细则给这篇作答评分、纠错、写润色稿，并通过 submit_evaluation 写回",
	"polish_essay":    "只润色不打分：以纠错边注（可一键采纳）的形式给出最小必要修改",
	"review_my_marks": "逐条回应用户自己在正文上做的标记：回答疑问、对问题给出纠错建议",
}

// PromptText 生成指定提示词的完整文本。
func PromptText(name string, d EssayDetail) (string, error) {
	e := d.Essay
	spec, ok := LookupType(e.Type)
	if !ok {
		return "", &ValidationError{Msg: fmt.Sprintf("未知题型：%s", e.Type)}
	}
	var steps, guide string
	switch name {
	case "grade_essay":
		steps = fmt.Sprintf(`请为 EssayPilot 中的作答（essay_id = %[1]s）评分。服务端不调用模型，评分由你完成，再用 essaypilot MCP 工具写回。

【操作步骤】
1. 调用 get_essay(id="%[1]s") 确认最新正文（下方附的是生成提示词时的快照，若不一致以 get_essay 为准）；可用 list_annotations(essay_id="%[1]s", author="user") 查看用户自己的标记，评分时一并回应。
2. 对照下方阅卷细则（也可调用 get_rubric(essay_id="%[1]s") 获取）逐维评分，确定档次。
3. 调用 submit_evaluation(essay_id="%[1]s", ...) 一次性提交：score、band、dimensions 四维（content/language/structure/convention，各含 score 与 feedback）、overall_comment、strengths、weaknesses、polished、corrections。四维之和必须等于 score，每维不超过自己的满分；被拒时按错误信息修正后重新提交。
4. 如有值得单独指出的亮点或问题，可再调用 annotate 补充 good / issue 边注（quote 必须逐字摘自正文；note 一句话点明作用或问题，不解释字面意思）。`, e.ID)
		guide = "【阅卷细则】\n" + Rubric(spec)
	case "polish_essay":
		steps = fmt.Sprintf(`请润色 EssayPilot 中的作答（essay_id = %[1]s），只改不打分。服务端不调用模型，润色由你完成，再用 essaypilot MCP 工具写回。

【操作步骤】
1. 调用 get_essay(id="%[1]s") 确认最新正文（下方附的是生成提示词时的快照，若不一致以 get_essay 为准）。
2. 按下方润色要求，找出必须修改之处。不要直接改写正文（不要调用 update_essay）。
3. 调用 annotate(essay_id="%[1]s", items=[...]) 批量提交修改建议：每条 kind="correction"，quote 为逐字摘自正文的最小片段（同一片段多次出现时用 occurrence 指明第几处），suggestion 为替换后的文本，label 可写纠错类别（%[2]s），note 一句话说明错在哪，不复述原文。
4. 检查返回结果，未找到原文的条目修正 quote 后重新提交。用户会在界面上逐条采纳（也可在用户要求时调用 apply_suggestions 一次性采纳）。`, e.ID, correctionKindList(spec.Subject))
		guide = "【润色要求】\n" + polishGuide(spec)
	case "review_my_marks":
		steps = fmt.Sprintf(`用户在 EssayPilot 的作答（essay_id = %[1]s）上做了标记（author = user），代表他们的疑问或关注点。请逐条回应。

【操作步骤】
1. 调用 list_annotations(essay_id="%[1]s", author="user") 获取用户标记的最新列表（下方附有生成提示词时的快照）。
2. 逐条处理，不要删除或修改用户的标记：
   - kind=question（疑问）或带有问题的 note：调用 reply(annotation_id=..., note=...) 直接回答，说清原因并给出可照搬的写法。
   - kind=issue（问题）：先用 reply 说明问题所在；如能改，再调用 annotate 在同一 quote 上提交 kind="correction" 的修改建议（带 suggestion，用户可一键采纳）。
   - kind=highlight / good：用 reply 简短确认好在哪里，或指出可以更进一步之处。
3. 回答要具体、对照考研阅卷标准，不写空话。`, e.ID)
		guide = "【参考要求】\n" + polishGuide(spec) + "\n\n【用户标记快照】\n" + userMarksText(d)
	default:
		return "", &ValidationError{Msg: fmt.Sprintf("未知提示词：%s（可选 %s）", name, strings.Join(PromptNames, " / "))}
	}

	return strings.Join([]string{steps, guide, essayInfoText(spec, e)}, "\n\n"), nil
}

func essayInfoText(spec TypeSpec, e Essay) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【作答信息】\n- essay_id：%s\n- 标题：%s\n- 题型：%s（满分 %g，字数要求 %d~%d %s）\n",
		e.ID, e.Title, spec.Label, spec.MaxScore, spec.MinLength, spec.MaxLength, LengthUnit(spec.Subject))
	if e.Category != "" {
		fmt.Fprintf(&b, "- 分类：%s\n", e.Category)
	}
	fmt.Fprintf(&b, "- 当前字数：%d %s\n", e.WordCount, LengthUnit(spec.Subject))
	prompt := strings.TrimSpace(e.Prompt)
	if prompt == "" {
		prompt = "（未填写）"
	}
	if e.PromptImage != "" {
		prompt += "\n（题目另附图片，见界面）"
	}
	content := strings.TrimSpace(e.Content)
	if content == "" {
		content = "（正文为空）"
	}
	fmt.Fprintf(&b, "\n【题目】\n%s\n\n【正文】\n%s", prompt, content)
	return b.String()
}

func userMarksText(d EssayDetail) string {
	var b strings.Builder
	n := 0
	for _, a := range d.Annotations {
		if a.Author != "user" || a.ParentID != nil {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. [%s] id=%s 「%s」", n, a.Kind, a.ID, a.Quote)
		if a.Note != "" {
			fmt.Fprintf(&b, " —— %s", a.Note)
		}
		if a.Status != StatusOpen {
			fmt.Fprintf(&b, "（%s）", a.Status)
		}
		b.WriteString("\n")
	}
	if n == 0 {
		return "（暂无用户标记）"
	}
	return strings.TrimRight(b.String(), "\n")
}
