// 词级 / 字级 diff（LCS）与编辑区间计算。

const RE = {
  english: /[a-zA-Z0-9'-]+|\s+|[^\s]/g,
  politics: /[一-龥]|[a-zA-Z0-9'-]+|\s+|[^\s]/g,
};

export function tokenize(text, subject) {
  return String(text || '').match(new RegExp((RE[subject] || RE.english).source, 'g')) || [];
}

// 返回 [{op:'eq'|'del'|'ins', text}]，相邻同类合并。
export function diffTokens(a, b) {
  let pre = 0;
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++;
  let suf = 0;
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++;
  const A = a.slice(pre, a.length - suf);
  const B = b.slice(pre, b.length - suf);
  const n = A.length, m = B.length;
  const ops = [];
  const push = (op, text) => {
    const last = ops[ops.length - 1];
    if (last && last.op === op) last.text += text;
    else ops.push({ op, text });
  };
  if (pre) push('eq', a.slice(0, pre).join(''));
  if (n && m) {
    // dp[i][j] = A[i..] 与 B[j..] 的 LCS 长度
    const w = m + 1;
    const dp = new Uint16Array((n + 1) * w);
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        dp[i * w + j] = A[i] === B[j] ? dp[(i + 1) * w + j + 1] + 1 : Math.max(dp[(i + 1) * w + j], dp[i * w + j + 1]);
      }
    }
    let i = 0, j = 0;
    while (i < n && j < m) {
      if (A[i] === B[j]) { push('eq', A[i]); i++; j++; }
      else if (dp[(i + 1) * w + j] >= dp[i * w + j + 1]) { push('del', A[i]); i++; }
      else { push('ins', B[j]); j++; }
    }
    while (i < n) push('del', A[i++]);
    while (j < m) push('ins', B[j++]);
  } else {
    if (n) push('del', A.join(''));
    if (m) push('ins', B.join(''));
  }
  if (suf) push('eq', a.slice(a.length - suf).join(''));
  return absorbWhitespace(ops);
}

// 把夹在两处改动之间的纯空白并入改动，避免 “del ␣ del” 碎片。
function absorbWhitespace(ops) {
  const out = [];
  for (let k = 0; k < ops.length; k++) {
    const o = ops[k];
    if (o.op === 'eq' && /^\s+$/.test(o.text) && !o.text.includes('\n') && k > 0 && k < ops.length - 1 && ops[k - 1].op !== 'eq' && ops[k + 1].op !== 'eq') {
      out.push({ op: 'del', text: o.text }, { op: 'ins', text: o.text });
    } else out.push({ ...o });
  }
  // 重新合并：把连续的 del/ins 段整理为 先 del 后 ins
  const merged = [];
  let dels = '', inss = '';
  const flush = () => {
    if (dels) merged.push({ op: 'del', text: dels });
    if (inss) merged.push({ op: 'ins', text: inss });
    dels = inss = '';
  };
  for (const o of out) {
    if (o.op === 'del') dels += o.text;
    else if (o.op === 'ins') inss += o.text;
    else { flush(); merged.push(o); }
  }
  flush();
  return merged;
}

export function diffText(a, b, subject) {
  return diffTokens(tokenize(a, subject), tokenize(b, subject));
}

export function countChanges(ops) {
  let n = 0;
  for (let k = 0; k < ops.length; k++) {
    if (ops[k].op !== 'eq' && (k === 0 || ops[k - 1].op === 'eq')) n++;
  }
  return n;
}

// 单处编辑区间：old[start, oldEnd) 被替换为 new[start, newEnd)。
export function editSpan(oldText, newText) {
  const max = Math.min(oldText.length, newText.length);
  let s = 0;
  while (s < max && oldText.charCodeAt(s) === newText.charCodeAt(s)) s++;
  let e = 0;
  while (e < max - s && oldText.charCodeAt(oldText.length - 1 - e) === newText.charCodeAt(newText.length - 1 - e)) e++;
  return { start: s, oldEnd: oldText.length - e, newEnd: newText.length - e, delta: newText.length - oldText.length };
}

// 按编辑区间平移一个偏移。isEnd 决定落在改动内部时的归属。
// 标注起点：恰在插入点上时随插入右移；终点：恰在插入点上时不扩张。
export function mapOffset(p, span, isEnd) {
  if (isEnd) {
    if (p <= span.start) return p;
    if (p >= span.oldEnd) return p + span.delta;
    return span.newEnd;
  }
  if (p >= span.oldEnd) return p + span.delta;
  if (p <= span.start) return p;
  return span.start;
}

// 平移标注偏移（返回新数组；不改原对象）。
export function shiftAnnotations(anns, oldText, newText) {
  if (oldText === newText) return anns;
  const span = editSpan(oldText, newText);
  return anns.map((a) => {
    if (a.status === 'resolved') return a;
    const start = mapOffset(a.start, span, false);
    const end = Math.max(start, mapOffset(a.end, span, true));
    return start === a.start && end === a.end ? a : { ...a, start, end };
  });
}
