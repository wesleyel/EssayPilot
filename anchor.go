package main

// 标注锚定：偏移一律为 UTF-16 码元（与浏览器 textarea.selectionStart 一致），左闭右开。
// 每条标注记下 quote 与前后各最多 32 个码元的上下文；正文改动后据此重新定位。

import (
	"slices"
	"unicode/utf16"
)

const anchorContext = 32

func u16(s string) []uint16        { return utf16.Encode([]rune(s)) }
func s16(u []uint16) string        { return string(utf16.Decode(u)) }
func isLowSurrogate(c uint16) bool { return c >= 0xDC00 && c <= 0xDFFF }

// checkRange 校验 [start,end) 非空、在正文内且不切开代理对。
func checkRange(t []uint16, start, end int) error {
	if start < 0 || end > len(t) || start >= end {
		return validationf("区间 [%d,%d) 无效（正文长度 %d）", start, end, len(t))
	}
	if isLowSurrogate(t[start]) || (end < len(t) && isLowSurrogate(t[end])) {
		return validationf("区间 [%d,%d) 切开了一个字符", start, end)
	}
	return nil
}

// setAnchor 把标注定位到 [start,end)，重算 quote/prefix/suffix。
func setAnchor(a *Annotation, t []uint16, start, end int) {
	ps := max(0, start-anchorContext)
	if ps > 0 && isLowSurrogate(t[ps]) {
		ps++
	}
	se := min(len(t), end+anchorContext)
	if se < len(t) && isLowSurrogate(t[se]) {
		se--
	}
	a.Start, a.End = start, end
	a.Quote = s16(t[start:end])
	a.Prefix = s16(t[ps:start])
	a.Suffix = s16(t[end:se])
}

// findAll 返回 q 在 t 中所有出现的起点（允许重叠）。
func findAll(t, q []uint16) []int {
	var out []int
	if len(q) == 0 {
		return out
	}
	for i := 0; i+len(q) <= len(t); i++ {
		if t[i] == q[0] && slices.Equal(t[i:i+len(q)], q) {
			out = append(out, i)
		}
	}
	return out
}

// locateQuote 找 quote 的第 occurrence 处（从 1 开始，0 视为 1）。
func locateQuote(t []uint16, quote string, occurrence int) (int, int, error) {
	if quote == "" {
		return 0, 0, validationf("quote 不能为空")
	}
	q := u16(quote)
	occ := findAll(t, q)
	if occurrence <= 0 {
		occurrence = 1
	}
	if len(occ) == 0 {
		return 0, 0, validationf("正文中找不到「%s」", quote)
	}
	if occurrence > len(occ) {
		return 0, 0, validationf("「%s」在正文中只出现 %d 次", quote, len(occ))
	}
	p := occ[occurrence-1]
	return p, p + len(q), nil
}

// textEdit：旧文 [start,end) 被替换为 newLen 个码元。
type textEdit struct{ start, end, newLen int }

// diffEdit 用公共前后缀把整篇改动近似为一处替换。
func diffEdit(old, cur []uint16) textEdit {
	p := 0
	for p < len(old) && p < len(cur) && old[p] == cur[p] {
		p++
	}
	s, maxS := 0, min(len(old), len(cur))-p
	for s < maxS && old[len(old)-1-s] == cur[len(cur)-1-s] {
		s++
	}
	return textEdit{p, len(old) - s, len(cur) - p - s}
}

// mapRange 把旧区间按 edits（按 start 升序、互不重叠）平移到新文；区间内部被改动则 ok=false。
func mapRange(edits []textEdit, s, e int) (int, int, bool) {
	delta := 0
	for _, ed := range edits {
		switch {
		case ed.end <= s:
			delta += ed.newLen - (ed.end - ed.start)
		case ed.start >= e:
		default:
			return s, e, false
		}
	}
	return s + delta, e + delta, true
}

func commonPrefixLen(a, b []uint16) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffixLen(a, b []uint16) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// relocate 重新定位一条顶层标注。先看平移后的原区间是否仍是 quote；
// 否则在新文中找 quote 的全部出现，按上下文吻合长度（主）+ 与原位置距离（次）取最优；
// 找不到则 orphaned 并保留原偏移。
func relocate(a *Annotation, t []uint16, edits []textEdit) {
	hint := a.Start
	if s, e, ok := mapRange(edits, a.Start, a.End); ok {
		hint = s
		if s >= 0 && s < e && e <= len(t) && s16(t[s:e]) == a.Quote {
			setAnchor(a, t, s, e)
			a.Status = StatusOpen
			return
		}
	}
	q := u16(a.Quote)
	occ := findAll(t, q)
	if len(occ) == 0 {
		a.Status = StatusOrphaned
		return
	}
	prefix, suffix := u16(a.Prefix), u16(a.Suffix)
	best, bestScore := occ[0], -1.0
	for _, p := range occ {
		ctx := commonSuffixLen(prefix, t[:p]) + commonPrefixLen(suffix, t[p+len(q):])
		dist := p - hint
		if dist < 0 {
			dist = -dist
		}
		score := float64(ctx) - float64(dist)/float64(len(t)+1)
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	setAnchor(a, t, best, best+len(q))
	a.Status = StatusOpen
}

// reanchor 在正文改为 text 后就地重新锚定 anns，返回位置或状态有变化的标注下标。
// resolved 的不动；回复跟随父标注的区间与状态。
func reanchor(anns []Annotation, text string, edits []textEdit) []int {
	t := u16(text)
	type key struct {
		start, end     int
		quote, pre, su string
		status         string
	}
	keyOf := func(a Annotation) key { return key{a.Start, a.End, a.Quote, a.Prefix, a.Suffix, a.Status} }
	before := make([]key, len(anns))
	byID := map[string]int{}
	for i, a := range anns {
		before[i] = keyOf(a)
		byID[a.ID] = i
	}
	for i := range anns {
		if anns[i].ParentID == nil && anns[i].Status != StatusResolved {
			relocate(&anns[i], t, edits)
		}
	}
	for i := range anns {
		if anns[i].ParentID == nil {
			continue
		}
		if p, ok := byID[*anns[i].ParentID]; ok {
			followParent(&anns[i], anns[p])
		}
	}
	var changed []int
	for i, a := range anns {
		if keyOf(a) != before[i] {
			changed = append(changed, i)
		}
	}
	return changed
}

func followParent(r *Annotation, p Annotation) {
	r.Start, r.End, r.Quote, r.Prefix, r.Suffix, r.Status = p.Start, p.End, p.Quote, p.Prefix, p.Suffix, p.Status
}

// replacement：旧文 [start,end) 换成 text。
type replacement struct {
	start, end int
	text       string
}

// applyReplacements 应用按 start 升序、互不重叠的替换，返回新文、对应的 edits 以及每处替换在新文中的区间。
func applyReplacements(t []uint16, reps []replacement) (string, []textEdit, [][2]int) {
	var out []uint16
	edits := make([]textEdit, len(reps))
	ranges := make([][2]int, len(reps))
	last := 0
	for i, r := range reps {
		out = append(out, t[last:r.start]...)
		repl := u16(r.text)
		ranges[i] = [2]int{len(out), len(out) + len(repl)}
		out = append(out, repl...)
		edits[i] = textEdit{r.start, r.end, len(repl)}
		last = r.end
	}
	out = append(out, t[last:]...)
	return s16(out), edits, ranges
}
