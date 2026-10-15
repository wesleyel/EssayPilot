package main

// 阅卷细则。服务端不调用模型：这些文本通过 MCP 的 get_rubric / prompts
// 交给本地 Agent，由 Agent 评分后调用 submit_evaluation 写回。

import (
	"fmt"
	"strings"
)

const englishBands = `- 第五档（高分档，A节 9-10分 / B节 17-20分）：很好地完成试题任务；包含所有内容要点；语法结构和词汇丰富、地道流畅，语言错误极少；有效采用多种衔接手法，文字连贯，层次清晰；格式和语域完全恰当贴切；对目标读者完全产生预期效果。
- 第四档（良好档，A节 7-8分 / B节 13-16分）：较好地完成任务；包含所有要点（允许漏1-2个次重点）；语言基本准确，语法结构和词汇较丰富，仅在尝试复杂结构或高级表达时有个别错误；组织严密，层次清晰；格式和语域较恰当。
- 第三档（及格档，A节 5-6分 / B节 9-12分）：基本完成任务；虽漏掉部分内容，但包含多数内容要点；应用的语法和词汇能满足需求，有一些语言错误但不影响理解；采用简单衔接，层次较清晰；格式语域基本合理。
- 第二档（不及格档，A节 3-4分 / B节 5-8分）：未能按要求完成任务；漏掉或未有效阐述内容要点，写无关内容；语法单调，词汇有限，较多错误影响理解；缺少连贯性；格式语域不恰当。
- 第一档（极低档，A节 1-2分 / B节 1-4分）：明显遗漏主要内容，大量不相关内容；语言错误极多且单调重复，严重有碍理解；无分段与衔接；无格式语域概念。
- 零档（0分）：信息太少，所写与要求无关，或无法辨认。`

const politicsBands = `- 第一档（高分档，8.5-10分）：原理定性精准，核心采分点全部命中；"原理表述+材料剖析"水乳交融，杜绝两张皮；政治立场坚定，话语规范严谨；分小问、标序号①②③，逻辑递进。
- 第二档（良好档，7.0-8.0分）：核心原理准确，命中绝大部分采分点；能较好联系材料；表述基本规范，偶有口语化或材料引用较浅；分点清晰，结构完整。
- 第三档（及格档，5.0-6.5分）：原理方向大致正确，但表述不全或有瑕疵，漏掉重要采分点；材料结合生硬；口语化较多；条理欠缺。
- 第四档（偏弱档，3.0-4.5分）：原理选择偏离或张冠李戴；纯抄材料或空谈口号；常识错误或逻辑混乱；缺乏分点规范。
- 第五档（极低分档，1.0-2.5分）：完全跑题，无有效采分点，答非所问。
- 零档（0分）：空白卷或完全无关内容。`

const vocabularyRules = `【词汇严格限定在考研大纲 5500 词范围内】
- 考研英语写作不是 GRE、托福或文学创作考试，所有替换与范文必须使用考研大纲 5500 词及四六级核心词汇。
- 严禁冷门、超纲、晦涩词汇，如 tranquility, mindfulness, serendipity, quintessence, epistemology, vicarious, fathomless, panacea, contemplation。
- 表达必须接地气、考场可复现：例如心态或文化类话题，使用 peace of mind, inner calm, spiritual satisfaction, cultural tradition, relax both body and mind 等大纲内常见表达。`

const minimalEditRules = `【润色核心红线：最小必要修改原则，杜绝无意义的同义词替换】
- polished 是"在考生原稿基础上的精准除虫与靶向提分"，绝不是另写一篇。
- 凡是语法正确、表达地道通顺的词句，必须 100% 原样保留。
- 典型反面教材（坚决禁止）：把 confident 改为 sure；把 offer some practical advice 改为 share a few practical suggestions；把 will participate in 改为 have entered；把 First, as for the poem selection 改为 When it comes to poem selection；把 central theme 改为 emotional tone；擅自改写或臆造考生提到的细节。
- 必改：拼写错误、语法硬伤（时态、主谓、单复数）、明显中式直译、逻辑不通的病句。
- 微调：仅当连续简单句极其单调时做适度从句/非谓语衔接，必须保留原句主干与原意。
- polished 中的每一处改动都应在 corrections 中有对应项；不得在 corrections 之外大面积换词。`

const politicsPolishRules = `【纠偏诊断 corrections】
- 只收录会导致失分的问题：概念混淆或原理错误、原理表述残缺、漏掉采分点、材料脱节、分点混乱、口语化到影响采分的表述。
- 每条 explanation 必须写明对应的采分点或原理，以及原文因此会丢分的原因；已经规范准确的表述不要收录。

【规范示范答卷 polished】
- 必须是可以直接背诵、考场拿 9-10 分的标准答案；按设问 (1)、(2) 分小段。
- 每小问采用【核心原理定性】+【结合材料辩证剖析】+【方法论启示/现实意义】的结构，用 ①②③ 标明采分点。
- 以考生作答为底稿：考生已经写对、写规范的原理表述和材料分析原样保留；改动只用于纠正错误、补上缺失的采分点、把原理和材料扣紧、理顺分点层次。
- 不得把已经规范的句子换个说法重写一遍（如"至关重要"改"十分关键"、"推动"改"促进"），也不得为显得高级堆砌与本题无关的时政口号。`

const substanceRules = `【改写与建议的底线：要么不改，要么改出实质】
- 每一处替换、改写或建议都必须解决一个说得出名字的问题：错误、歧义、跑题、空泛、中式表达、逻辑断裂、漏掉要点或采分点。说不出具体问题的地方一律保留原文，不提建议。
- 禁止同义词洗稿：意思、语气、信息量都与原文相同，只是换个说法的替换一律视为无效输出。例如 important→vital、help→assist、think→hold the view that、很重要→至关重要、搞好→做好。
- 禁止空泛理由：不得只写"更地道""更高级""更规范""更有文采"。理由必须指出原文具体错在或弱在哪里，以及改后具体多了什么（纠正了什么错误、补上了哪个要点、理顺了哪层逻辑）。
- 替换必须切题：放回原句后语法、语义和上下文衔接都成立，不改变考生原意，不臆造考生没写的事实。
- 宁缺毋滥：写得好的地方就不提；没有值得改的内容时对应数组返回 []，绝不为凑数量编造条目。`

const annotationRules = `【标注与边注】
- 标注直接挂在原文上，界面以 Tufte 式边注显示在正文右侧。quote 必须逐字摘自当前正文（包括标点），尽量短而唯一；同一短语多次出现时用 occurrence（从 1 开始）指明第几处。
- kind：highlight 重点 / good 亮点 / issue 问题 / question 疑问 / correction 纠错（必须带 suggestion，用户可一键采纳）。
- note 是边注正文，一两句话说清具体问题或好在哪里，不写空话。
- 用户自己做的标记（author = user）代表他们的疑问或关注点，回应时用新的标注或在评分总评中回答，不要删除用户的标记。`

func dimensionRubric(spec TypeSpec) string {
	var b strings.Builder
	for _, key := range DimensionKeys {
		d := Dimensions[spec.Subject][key]
		fmt.Fprintf(&b, "- dimensions.%s（%s，满分 %g）：%s\n", key, d.Label, dimensionMax(spec.Subject, spec.MaxScore, key), d.Criteria)
	}
	return strings.TrimRight(b.String(), "\n")
}

func correctionKindList(subject Subject) string {
	parts := []string{}
	for _, k := range CorrectionKinds[subject] {
		parts = append(parts, fmt.Sprintf("%s（%s）", k, CorrectionLabels[k]))
	}
	return strings.Join(parts, "、")
}

// rubric 返回某题型完整的阅卷细则，供 Agent 评分时遵循。
func rubric(spec TypeSpec) string {
	unit := lengthUnit(spec.Subject)
	submit := fmt.Sprintf(`【提交方式】评完后调用 submit_evaluation：
- score 为总分（满分 %g），四个维度 dimensions.{%s} 各含 score 与 feedback，维度得分之和必须等于 score，且每个维度不超过自己的满分。
- band 写档次（如 第四档 / 高分档）；overallComment 约 200 字考官总评；strengths / weaknesses 各写具体条目。
- corrections 每项 {quote, corrected, kind, explanation}：quote 逐字摘自正文，kind 只能取 %s。它们会作为纠错边注挂到原文上。
- polished 为整篇修订稿。`, spec.MaxScore, strings.Join(DimensionKeys, ","), correctionKindList(spec.Subject))

	if spec.Subject == English {
		return strings.Join([]string{
			"你是一名资深的考研英语（英语一/英语二）官方阅卷组专家与写作教练。请严格对照教育部考研英语大纲作文评分标准，对考生草稿进行分档打分、精细纠错，并生成高分润色范文。",
			"【官方评分档次标准】\n" + englishBands,
			fmt.Sprintf("【试卷信息】\n- 试卷板块：%s\n- 字数要求：%d~%d %s", spec.ExamName, spec.MinLength, spec.MaxLength, unit),
			fmt.Sprintf("【四维评分细则（满分 %g 分）】\n%s", spec.MaxScore, dimensionRubric(spec)),
			vocabularyRules, substanceRules, minimalEditRules, annotationRules, submit,
		}, "\n\n")
	}
	return strings.Join([]string{
		"你是考研思想政治理论（101）官方阅卷组专家，同时是考研政治高分辅导名师。请严格对照考研政治主观分析题阅卷采分标准，对考生作答进行采点评分、诊断偏误，并生成满分规范示范答卷。",
		fmt.Sprintf("【主观分析题评分档次（单题满分 %g 分）】\n%s", spec.MaxScore, politicsBands),
		fmt.Sprintf("【试题信息】\n- 题型模块：%s\n- 建议作答字数：%d~%d %s（分点论述）", spec.ExamName, spec.MinLength, spec.MaxLength, unit),
		fmt.Sprintf("【四维阅卷细则（必须写入对应键，禁止对调）】\n%s", dimensionRubric(spec)),
		politicsPolishRules, substanceRules, annotationRules, submit,
	}, "\n\n")
}

// polishGuide 只润色、不打分时的要求。
func polishGuide(spec TypeSpec) string {
	parts := []string{substanceRules, annotationRules}
	if spec.Subject == English {
		parts = append([]string{vocabularyRules, minimalEditRules}, parts...)
	} else {
		parts = append([]string{politicsPolishRules}, parts...)
	}
	return strings.Join(parts, "\n\n")
}
