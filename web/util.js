// 小工具：转义、图标（Lucide 路径）、提示、确认框、标签输入、图片压缩。

export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const ICONS = {
  menu: '<path d="M4 12h16"/><path d="M4 6h16"/><path d="M4 18h16"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>',
  copy: '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  right: '<path d="m9 18 6-6-6-6"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  image: '<rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>',
  pen: '<path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  history: '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
};

export function icon(name, size = 16) {
  return `<svg class="ico" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ''}</svg>`;
}

export function debounce(fn, ms) {
  let t;
  const d = (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
  d.cancel = () => clearTimeout(t);
  return d;
}

export function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  const p = (n) => String(n).padStart(2, '0');
  const today = new Date();
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}`;
  return d.toDateString() === today.toDateString() ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

export const fmtScore = (n) => (n === null || n === undefined ? '—' : String(Math.round(n * 10) / 10));

export const SUBJECT_LABEL = { english: '英语', politics: '政治' };

const SHORT_DIM = { 政治话语体系: '话语', 论述逻辑层次: '论述', 材料结合深度: '材料', 原理与采分点: '原理' };
export function shortDim(label) {
  if (!label) return '';
  if (SHORT_DIM[label]) return SHORT_DIM[label];
  return label.split('与')[0].slice(0, 2);
}

export function authorName(a) {
  if (!a || a === 'user') return '我';
  return a.startsWith('agent:') ? a.slice(6) || 'Agent' : a;
}
export const isAgent = (a) => typeof a === 'string' && a.startsWith('agent:');

let toastTimer;
export function toast(msg, kind = '') {
  let el = document.getElementById('toast');
  if (!el) {
    el = document.createElement('div');
    el.id = 'toast';
    el.setAttribute('role', 'status');
    document.body.appendChild(el);
  }
  el.textContent = msg;
  el.className = 'show ' + kind;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = ''; }, kind === 'err' ? 4200 : 2200);
}

export function errToast(e) {
  console.error(e);
  toast(e && e.message ? e.message : String(e), 'err');
}

// 模态框：content 为 HTML；返回 { el, close }。
export function openDialog(title, bodyHTML, actionsHTML) {
  const back = document.createElement('div');
  back.className = 'dialog-backdrop';
  back.innerHTML = `<div class="dialog" role="dialog" aria-modal="true" aria-label="${esc(title)}">
    <div class="dialog-title">${esc(title)}</div>
    <div class="dialog-body">${bodyHTML}</div>
    <div class="dialog-actions">${actionsHTML}</div></div>`;
  document.body.appendChild(back);
  const close = () => { back.remove(); document.removeEventListener('keydown', onKey, true); };
  const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); close(); back.dispatchEvent(new Event('cancel')); } };
  document.addEventListener('keydown', onKey, true);
  back.addEventListener('mousedown', (e) => { if (e.target === back) { close(); back.dispatchEvent(new Event('cancel')); } });
  return { el: back, close };
}

export function confirmDialog(title, body, okLabel = '确定') {
  return new Promise((resolve) => {
    const d = openDialog(title, esc(body), `<button class="btn btn-secondary" data-r="0">取消</button><button class="btn btn-primary" data-r="1">${esc(okLabel)}</button>`);
    d.el.addEventListener('cancel', () => resolve(false));
    d.el.querySelectorAll('[data-r]').forEach((b) => b.addEventListener('click', () => { d.close(); resolve(b.dataset.r === '1'); }));
    d.el.querySelector('[data-r="1"]').focus();
  });
}

// 标签 chip 输入。onChange(tags) 在增删时调用。
export function tagInput(el, tags, onChange) {
  let list = [...(tags || [])];
  const render = () => {
    el.innerHTML = list.map((t, i) => `<span class="chip">#${esc(t)}<button type="button" data-i="${i}" aria-label="删除标签 ${esc(t)}">${icon('x', 12)}</button></span>`).join('') +
      `<input class="chip-input" placeholder="${list.length ? '' : '添加标签，回车确认'}" aria-label="添加标签">`;
    const input = el.querySelector('input');
    input.addEventListener('keydown', (e) => {
      if ((e.key === 'Enter' || e.key === ',' || e.key === '，') && !e.isComposing) {
        e.preventDefault();
        add(input.value);
      } else if (e.key === 'Backspace' && !input.value && list.length) {
        list.pop(); render(); onChange && onChange(list); el.querySelector('input').focus();
      }
    });
    input.addEventListener('blur', () => { if (input.value.trim()) add(input.value, false); });
  };
  const add = (v, refocus = true) => {
    const t = v.replace(/^#/, '').trim();
    if (t && !list.includes(t)) { list.push(t); onChange && onChange(list); }
    render();
    if (refocus) el.querySelector('input').focus();
  };
  el.classList.add('chips');
  el.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-i]');
    if (b) { list.splice(+b.dataset.i, 1); render(); onChange && onChange(list); return; }
    if (e.target === el) el.querySelector('input').focus();
  });
  render();
  return { get: () => { const i = el.querySelector('input'); if (i && i.value.trim()) add(i.value, false); return [...list]; }, set: (t) => { list = [...t]; render(); } };
}

// 把图片文件压缩成 ≤ maxBytes 的 jpeg data URL。
export async function compressImage(file, maxBytes = 1.2 * 1024 * 1024) {
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise((res, rej) => { const i = new Image(); i.onload = () => res(i); i.onerror = () => rej(new Error('图片无法读取')); i.src = url; });
    let scale = Math.min(1, 2000 / Math.max(img.naturalWidth, img.naturalHeight));
    for (let round = 0; round < 6; round++) {
      const c = document.createElement('canvas');
      c.width = Math.max(1, Math.round(img.naturalWidth * scale));
      c.height = Math.max(1, Math.round(img.naturalHeight * scale));
      const ctx = c.getContext('2d');
      ctx.fillStyle = '#fff';
      ctx.fillRect(0, 0, c.width, c.height);
      ctx.drawImage(img, 0, 0, c.width, c.height);
      for (const q of [0.86, 0.75, 0.62, 0.5]) {
        const data = c.toDataURL('image/jpeg', q);
        if (data.length * 0.75 <= maxBytes) return data;
      }
      scale *= 0.75;
    }
    throw new Error('图片太大，压缩后仍超过 1.2MB');
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const t = document.createElement('textarea');
    t.value = text;
    t.style.position = 'fixed';
    t.style.opacity = '0';
    document.body.appendChild(t);
    t.select();
    document.execCommand('copy');
    t.remove();
  }
}

// ---------------------------------------------------------------- Markdown（边注用的小子集）
// 先转义再套格式，不放行任何原始 HTML。支持：**粗体**、*斜体*、~~删除~~、`代码`、[链接](https://…)、
// 无序/有序列表、> 引用、# 标题（渲染为粗体行）、空行分段、单换行。

function inlineFmt(s) {
  return s
    .replace(/\*\*(?=\S)([^*]*?\S)\*\*/g, '<b>$1</b>')
    .replace(/(^|[^*\w])\*(?=\S)([^*]*?\S)\*(?!\*)/g, '$1<i>$2</i>')
    .replace(/~~(?=\S)(.*?\S)~~/g, '<s>$1</s>')
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
}

// 行内 Markdown → HTML。换行原样保留（由调用方决定怎么处理）。
export function mdInline(src) {
  const parts = String(src ?? '').split(/(`[^`\n]+`)/);
  return parts.map((p, i) => (i % 2 ? `<code>${esc(p.slice(1, -1))}</code>` : inlineFmt(esc(p)))).join('');
}

// 单行摘要：换行折成空格、去掉块级标记。
export function mdLine(src) {
  const s = String(src ?? '').split('\n').map((l) => l.replace(/^\s*(?:[-*+]\s+|\d+[.)]\s+|>\s?|#{1,6}\s+)/, '')).filter((l) => l.trim()).join(' ');
  return mdInline(s);
}

export function md(src) {
  const lines = String(src ?? '').replace(/\r\n?/g, '\n').split('\n');
  let html = '';
  let para = [];
  let list = null; // { tag, items }
  let quote = [];
  const flushPara = () => { if (para.length) html += `<p>${para.map(mdInline).join('<br>')}</p>`; para = []; };
  const flushList = () => { if (list) html += `<${list.tag}>${list.items.map((x) => `<li>${mdInline(x)}</li>`).join('')}</${list.tag}>`; list = null; };
  const flushQuote = () => { if (quote.length) html += `<blockquote>${quote.map(mdInline).join('<br>')}</blockquote>`; quote = []; };
  const flush = () => { flushPara(); flushList(); flushQuote(); };
  for (const line of lines) {
    let m;
    if (!line.trim()) { flush(); continue; }
    if ((m = line.match(/^\s*[-*+]\s+(.*)$/))) {
      flushPara(); flushQuote();
      if (!list || list.tag !== 'ul') { flushList(); list = { tag: 'ul', items: [] }; }
      list.items.push(m[1]);
    } else if ((m = line.match(/^\s*\d+[.)]\s+(.*)$/))) {
      flushPara(); flushQuote();
      if (!list || list.tag !== 'ol') { flushList(); list = { tag: 'ol', items: [] }; }
      list.items.push(m[1]);
    } else if ((m = line.match(/^\s*>\s?(.*)$/))) {
      flushPara(); flushList();
      quote.push(m[1]);
    } else if ((m = line.match(/^\s*#{1,6}\s+(.*)$/))) {
      flush();
      html += `<p><b>${mdInline(m[1])}</b></p>`;
    } else {
      flushList(); flushQuote();
      para.push(line);
    }
  }
  flush();
  return html;
}
