package main

// 科目、题型、分类、评分维度与标注的领域规则。
// 所有"按科目/题型分支"的数据都集中在这里查表，其余代码不写 if/else。

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

type Subject string

const (
	English  Subject = "english"
	Politics Subject = "politics"
)

var Subjects = []Subject{English, Politics}

// TypeSpec 是一个题型的考试规格。
type TypeSpec struct {
	ID         string   `json:"id"`
	Subject    Subject  `json:"subject"`
	Label      string   `json:"label"`
	ShortLabel string   `json:"shortLabel"`
	ExamName   string   `json:"examName"`
	MaxScore   float64  `json:"maxScore"`
	MinLength  int      `json:"minLength"`
	MaxLength  int      `json:"maxLength"`
	Categories []string `json:"categories"`
}

// Types 按展示顺序列出全部题型；题型 id 在科目间不重名。
var Types = []TypeSpec{
	{"part-a", English, "Part A 小作文", "Part A", "Part A 应用文书信/告示 (小作文，满分 10 分)", 10, 80, 120,
		[]string{"建议信", "推荐信", "邀请信", "致歉信", "申请求助信", "咨询回复", "通知"}},
	{"part-b", English, "Part B 大作文", "Part B", "Part B 短文写作图画/图表 (大作文，满分 20 分)", 20, 160, 200,
		[]string{"图画·人生态度", "图画·品德情感", "图画·教育成长", "图画·文化社会", "图表·柱状图", "图表·表格", "图表·组合图"}},
	{"mayuan", Politics, "34题 马原", "34题·马原", "马克思主义基本原理 (第34题·马原)", 10, 350, 450, []string{}},
	{"maozhongte", Politics, "35题 毛中特·新思想", "35题·毛中特", "毛泽东思想和中国特色社会主义理论体系与新时代思想 (第35题·毛中特)", 10, 350, 450, []string{}},
	{"shigang", Politics, "36题 史纲", "36题·史纲", "中国近现代史纲要 (第36题·史纲)", 10, 350, 450, []string{}},
	{"defa", Politics, "37题 德法", "37题·德法", "思想道德与法治 (第37题·德法)", 10, 350, 450, []string{}},
	{"dangdai", Politics, "38题 当代", "38题·当代", "当代世界经济与政治 (第38题·当代)", 10, 350, 450, []string{}},
}

func typeSpec(id string) (TypeSpec, bool) {
	for _, t := range Types {
		if t.ID == id {
			return t, true
		}
	}
	return TypeSpec{}, false
}

func validSubject(s Subject) bool { return slices.Contains(Subjects, s) }

// ---------- 分类与标签 ----------

const (
	MaxTags      = 10
	MaxTagLength = 20
)

func normalizeTags(tags []string) []string {
	out := []string{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !slices.Contains(out, tag) {
			out = append(out, tag)
		}
	}
	return out
}

func categoryError(spec TypeSpec, category string) error {
	if len(spec.Categories) == 0 {
		if category != "" {
			return fmt.Errorf("题型 %s 没有分类，category 请留空", spec.ID)
		}
		return nil
	}
	if !slices.Contains(spec.Categories, category) {
		return fmt.Errorf("请选择分类：%s", strings.Join(spec.Categories, " / "))
	}
	return nil
}

func tagsError(tags []string) error {
	if len(tags) == 0 {
		return fmt.Errorf("请至少添加一个主题标签")
	}
	if len(tags) > MaxTags {
		return fmt.Errorf("主题标签最多 %d 个", MaxTags)
	}
	for _, tag := range tags {
		if len([]rune(tag)) > MaxTagLength {
			return fmt.Errorf("主题标签「%s」超过 %d 字", tag, MaxTagLength)
		}
	}
	return nil
}

// ---------- 字数 ----------

var (
	cjkRe  = regexp.MustCompile(`[一-龥]`)
	wordRe = regexp.MustCompile(`[a-zA-Z0-9'-]+`)
)

// countWords：英语按词计数；政治按汉字计数，夹杂的英文/数字按词计。
func countWords(text string, subject Subject) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	if subject == English {
		return len(wordRe.FindAllString(text, -1))
	}
	cjk := len(cjkRe.FindAllString(text, -1))
	rest := len(wordRe.FindAllString(cjkRe.ReplaceAllString(text, " "), -1))
	return cjk + rest
}

func lengthUnit(subject Subject) string {
	if subject == English {
		return "词"
	}
	return "字"
}

// ---------- 评分维度 ----------

var DimensionKeys = []string{"content", "language", "structure", "convention"}

type DimensionSpec struct {
	Label    string  `json:"label"`
	Weight   float64 `json:"weight"`
	Criteria string  `json:"criteria"`
}

var Dimensions = map[Subject]map[string]DimensionSpec{
	English: {
		"content":    {"内容与切题", 0.25, "内容要点覆盖与切题度：是否遗漏提纲要求/图画图表核心信息；立意是否偏题跑题。"},
		"language":   {"语言表达", 0.25, "语法与词汇丰富度：词汇准确地道，杜绝中式直译；句式多样化（从句、非谓语、倒装等合理交替）。"},
		"structure":  {"结构与逻辑", 0.25, "篇章结构与连贯逻辑：段落推进自然，衔接词不生硬机械；层次严密。"},
		"convention": {"规范与格式", 0.25, "语言规范性与准确度：拼写、标点符号、时态主谓搭配、格式称谓与语域得体性。"},
	},
	Politics: {
		"content":    {"原理与采分点", 0.35, "核心概念与基本原理是否准确，是否踩中参考答案的采分关键词。"},
		"language":   {"政治话语体系", 0.2, "是否使用官方权威术语与时政金句，语言是否庄重凝练，有无白话、套话。"},
		"structure":  {"论述逻辑层次", 0.2, "是否按设问分段，是否用“①②③”或“第一、第二”分点，逻辑是否递进。"},
		"convention": {"材料结合深度", 0.25, "是否把原理和材料写在一起，有无“两张皮”，方法论启示是否落到本题。"},
	},
}

func dimensionMax(subject Subject, maxScore float64, key string) float64 {
	return math.Round(Dimensions[subject][key].Weight*maxScore*10) / 10
}

// ---------- 纠错类别 ----------

var CorrectionKinds = map[Subject][]string{
	English:  {"spelling", "grammar", "collocation", "polish"},
	Politics: {"concept", "analysis", "discourse", "structure", "grammar"},
}

var CorrectionLabels = map[string]string{
	"spelling":    "错别字",
	"grammar":     "语法语病",
	"collocation": "词句搭配",
	"polish":      "润色精炼",
	"concept":     "原理纠偏",
	"analysis":    "材料结合",
	"discourse":   "话语规范",
	"structure":   "层次分点",
}

// ---------- 标注 ----------

// AnnotationKinds：重点 / 亮点 / 问题 / 疑问 / 纠错。纠错带替换建议，可一键采纳。
var AnnotationKinds = []string{"highlight", "good", "issue", "question", "correction"}

var AnnotationKindLabels = map[string]string{
	"highlight":  "重点",
	"good":       "亮点",
	"issue":      "问题",
	"question":   "疑问",
	"correction": "纠错",
}

const (
	StatusOpen     = "open"
	StatusResolved = "resolved"
	StatusOrphaned = "orphaned"
)
