// 编辑与标记合一：透明 textarea 叠在镜像层上。镜像层渲染文字与 <mark>，textarea 只负责输入、光标与选区。
// 偏移一律为 JS 字符串索引（UTF-16 码元），与服务端一致。

import { esc, isAgent } from './util.js';

export function paragraphsOf(text) {
  const out = [];
  const re = /[^\n]+/g;
  let m;
  while ((m = re.exec(text))) {
    if (m[0].trim()) out.push({ i: out.length, start: m.index, end: m.index + m[0].length });
  }
  return out;
}

export function paragraphIndexAt(paras, offset) {
  let idx = 0;
  for (const p of paras) {
    if (offset >= p.start) idx = p.i;
    else break;
  }
  return idx;
}

export function markClass(a) {
  return `m k-${a.kind} ${isAgent(a.author) ? 'ag' : 'us'}`;
}

// 渲染 text[from, to) 的 HTML，叠加标注。opts.sups: Map(id → 编号) 时在标注末尾插入上标；
// opts.offsets: 为每段文字包 data-o（阅读视图用来把 DOM 选区换算成偏移）；opts.pending: {start,end} 临时选区。
export function renderMarked(text, from, to, anns, opts = {}) {
  const rel = anns.filter((a) => a.start < a.end && a.start < to && a.end > from);
  if (opts.pending && opts.pending.start < opts.pending.end && opts.pending.start < to && opts.pending.end > from) {
    rel.push({ id: '__pending', start: opts.pending.start, end: opts.pending.end, kind: 'pending', author: 'user', _pending: true });
  }
  const cuts = new Set([from, to]);
  for (const a of rel) {
    if (a.start > from && a.start < to) cuts.add(a.start);
    if (a.end > from && a.end < to) cuts.add(a.end);
  }
  const pts = [...cuts].sort((x, y) => x - y);
  let html = '';
  for (let k = 0; k < pts.length - 1; k++) {
    const x = pts[k], y = pts[k + 1];
    if (x >= y) continue;
    const cover = rel.filter((a) => a.start <= x && a.end >= y).sort((p, q) => (q.end - q.start) - (p.end - p.start));
    let open = '', close = '';
    for (const a of cover) {
      const cls = a._pending ? 'm pending' : markClass(a);
      open += `<mark class="${cls}" data-id="${esc(a.id)}">`;
      close = '</mark>' + close;
    }
    const seg = esc(text.slice(x, y));
    html += open + (opts.offsets ? `<span data-o="${x}">${seg}</span>` : seg) + close;
    if (opts.sups) {
      for (const a of rel) {
        if (a.end === y && opts.sups.has(a.id)) html += `<sup class="sn ${isAgent(a.author) ? 'ag' : 'us'}" data-id="${esc(a.id)}">${opts.sups.get(a.id)}</sup>`;
      }
    }
  }
  return html;
}

export class Editor {
  constructor(wrap, cb) {
    this.wrap = wrap;
    this.cb = cb;
    wrap.innerHTML = '<div class="mirror" aria-hidden="true"></div><div class="nums" aria-hidden="true"></div>' +
      '<textarea class="ta" spellcheck="false" autocapitalize="off" autocomplete="off" aria-label="作答正文"></textarea>';
    this.mirror = wrap.querySelector('.mirror');
    this.nums = wrap.querySelector('.nums');
    this.ta = wrap.querySelector('.ta');
    this.anns = [];
    this.numbers = new Map();
    this.pending = null;
    this.hotId = null;

    this.ta.addEventListener('input', () => cb.onInput(this.ta.value));
    this.ta.addEventListener('scroll', () => { this.ta.scrollTop = 0; this.ta.scrollLeft = 0; });
    const sel = () => cb.onSelection(this.selection());
    for (const ev of ['select', 'keyup', 'mouseup', 'focus']) this.ta.addEventListener(ev, sel);
    document.addEventListener('selectionchange', () => { if (document.activeElement === this.ta) sel(); });
    this.ta.addEventListener('mouseup', (e) => {
      const s = this.selection();
      if (s.end > s.start) cb.onRequestAnnotate(s, e);
    });
    this.ta.addEventListener('keyup', (e) => {
      const s = this.selection();
      if (e.key === 'Shift' && s.end > s.start) cb.onRequestAnnotate(s, e);
    });
    this.ta.addEventListener('keydown', (e) => cb.onKeyDown && cb.onKeyDown(e));
    let raf = 0;
    this.ta.addEventListener('mousemove', (e) => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        cb.onHover(this.idAtPoint(e.clientX, e.clientY));
      });
    });
    this.ta.addEventListener('mouseleave', () => cb.onHover(null));
    new ResizeObserver(() => cb.onLayout && cb.onLayout()).observe(this.mirror);
  }

  get value() { return this.ta.value; }

  setValue(text) {
    if (this.ta.value === text) return;
    const focused = document.activeElement === this.ta;
    const { start, end } = this.selection();
    this.ta.value = text;
    if (focused) {
      const s = Math.min(start, text.length), e = Math.min(end, text.length);
      this.ta.setSelectionRange(s, e);
    }
  }

  selection() {
    return { start: this.ta.selectionStart ?? 0, end: this.ta.selectionEnd ?? 0 };
  }

  render({ anns, numbers, pending, hotId }) {
    this.anns = anns;
    this.numbers = numbers;
    this.pending = pending;
    this.hotId = hotId;
    const text = this.ta.value;
    const paras = paragraphsOf(text);
    let html = '', pos = 0;
    for (const p of paras) {
      if (p.start > pos) html += esc(text.slice(pos, p.start));
      html += `<span class="para" data-p="${p.i}">${renderMarked(text, p.start, p.end, anns, { pending })}</span>`;
      pos = p.end;
    }
    if (pos < text.length) html += esc(text.slice(pos));
    html += '<span class="tail">​</span>';
    this.mirror.innerHTML = html;
    this.paras = paras;
    this.syncHeight();
    this.renderNums();
    this.setHot(hotId);
  }

  syncHeight() {
    this.ta.style.height = this.mirror.offsetHeight + 'px';
  }

  renderNums() {
    const base = this.wrap.getBoundingClientRect();
    let html = '';
    for (const [id, n] of this.numbers) {
      const els = this.mirror.querySelectorAll(`mark[data-id="${CSS.escape(id)}"]`);
      if (!els.length) continue;
      const rects = els[els.length - 1].getClientRects();
      if (!rects.length) continue;
      const r = rects[rects.length - 1];
      const a = this.anns.find((x) => x.id === id);
      html += `<span class="sn ${a && isAgent(a.author) ? 'ag' : 'us'}" data-id="${esc(id)}" style="left:${(r.right - base.left).toFixed(1)}px;top:${(r.top - base.top).toFixed(1)}px">${n}</span>`;
    }
    this.nums.innerHTML = html;
  }

  setHot(id) {
    this.hotId = id;
    for (const el of this.wrap.querySelectorAll('.hot')) el.classList.remove('hot');
    if (!id) return;
    for (const el of this.wrap.querySelectorAll(`[data-id="${CSS.escape(id)}"]`)) el.classList.add('hot');
  }

  idAtPoint(x, y) {
    const els = document.elementsFromPoint(x, y);
    for (const el of els) {
      if (el.matches && el.matches('mark.m[data-id]') && this.mirror.contains(el)) return el.dataset.id === '__pending' ? null : el.dataset.id;
    }
    return null;
  }

  // 段落几何（相对 wrap）。
  geometry() {
    const base = this.wrap.getBoundingClientRect();
    const out = [];
    for (const span of this.mirror.querySelectorAll('.para')) {
      const rs = span.getClientRects();
      if (!rs.length) continue;
      const i = +span.dataset.p;
      out.push({ ...this.paras[i], top: rs[0].top - base.top, bottom: rs[rs.length - 1].bottom - base.top });
    }
    return out;
  }

  markTop(id) {
    const el = this.mirror.querySelector(`mark[data-id="${CSS.escape(id)}"]`);
    if (!el) return null;
    const rs = el.getClientRects();
    if (!rs.length) return null;
    return rs[0].top - this.wrap.getBoundingClientRect().top;
  }

  // 返回偏移区间在视口中的矩形（取最后一行），用于定位浮层。
  rangeRect(start, end) {
    const walker = document.createTreeWalker(this.mirror, NodeFilter.SHOW_TEXT);
    let pos = 0, sNode = null, sOff = 0, eNode = null, eOff = 0, n;
    while ((n = walker.nextNode())) {
      if (n.parentElement.classList.contains('tail')) break;
      const len = n.data.length;
      if (!sNode && start <= pos + len) { sNode = n; sOff = start - pos; }
      if (end <= pos + len) { eNode = n; eOff = end - pos; break; }
      pos += len;
    }
    if (!sNode || !eNode) return this.wrap.getBoundingClientRect();
    const r = document.createRange();
    r.setStart(sNode, Math.max(0, Math.min(sOff, sNode.data.length)));
    r.setEnd(eNode, Math.max(0, Math.min(eOff, eNode.data.length)));
    const rs = r.getClientRects();
    return rs.length ? rs[rs.length - 1] : r.getBoundingClientRect();
  }

  focusAt(offset) {
    this.ta.focus({ preventScroll: true });
    this.ta.setSelectionRange(offset, offset);
  }
}

// 阅读视图（“落到段后”模式）：段落为块，段后两列边注；仍可选中文字做标注。
export function renderReading(container, text, anns, numbers, notesForPara, pending) {
  const paras = paragraphsOf(text);
  let html = '';
  for (const p of paras) {
    html += `<div class="rpara" data-p="${p.i}"><p class="essay-text" data-start="${p.start}">${renderMarked(text, p.start, p.end, anns, { sups: numbers, offsets: true, pending })}</p>${notesForPara(p)}</div>`;
  }
  if (!paras.length) html = '<p class="empty-body">正文为空。点“编辑正文”开始写作。</p>';
  container.innerHTML = html;
  return paras;
}

// 阅读视图里 DOM 选区 → 正文偏移。
export function readingSelection(container) {
  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
  const r = sel.getRangeAt(0);
  if (!container.contains(r.commonAncestorContainer)) return null;
  const toOffset = (node, off, isEnd) => {
    if (node.nodeType === Node.TEXT_NODE) {
      const holder = node.parentElement.closest('[data-o]');
      if (holder && container.contains(holder)) return +holder.dataset.o + off;
    }
    // 元素边界：找相邻的 data-o 文本
    const el = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
    const all = [...container.querySelectorAll('[data-o]')];
    if (!all.length) return null;
    const child = el.childNodes[off] || null;
    if (isEnd) {
      let best = null;
      for (const h of all) {
        const pos = child ? child.compareDocumentPosition(h) : el.compareDocumentPosition(h);
        if (child ? pos & Node.DOCUMENT_POSITION_PRECEDING : (pos & Node.DOCUMENT_POSITION_CONTAINED_BY || pos & Node.DOCUMENT_POSITION_PRECEDING)) best = h;
      }
      return best ? +best.dataset.o + best.textContent.length : null;
    }
    for (const h of all) {
      const pos = child ? child.compareDocumentPosition(h) : el.compareDocumentPosition(h);
      if (child ? (pos & Node.DOCUMENT_POSITION_FOLLOWING || h === child || child.contains(h)) : pos & Node.DOCUMENT_POSITION_FOLLOWING) return +h.dataset.o;
    }
    return null;
  };
  const start = toOffset(r.startContainer, r.startOffset, false);
  const end = toOffset(r.endContainer, r.endOffset, true);
  if (start === null || end === null || end <= start) return null;
  const rs = r.getClientRects();
  return { start, end, rect: rs.length ? rs[rs.length - 1] : r.getBoundingClientRect() };
}
