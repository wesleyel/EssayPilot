// 选区浮层：类型（重点/亮点/问题/疑问，键 1-4）+ 边注输入（支持 Markdown），↵ 保存 · ⇧↵ 换行 · Esc 取消。也用于回复与编辑边注。

import { esc } from './util.js';

const KINDS = [['highlight', '重点'], ['good', '亮点'], ['issue', '问题'], ['question', '疑问']];

let el = null;
let cur = null;

function ensure() {
  if (el) return el;
  el = document.createElement('div');
  el.className = 'popover';
  el.hidden = true;
  el.setAttribute('role', 'dialog');
  document.body.appendChild(el);
  el.addEventListener('mousedown', (e) => {
    if (e.target.closest('[data-kind]')) e.preventDefault();
  });
  el.addEventListener('click', (e) => {
    const b = e.target.closest('[data-kind]');
    if (b) { setKind(b.dataset.kind); el.querySelector('.pop-input').focus(); }
    const s = e.target.closest('[data-save]');
    if (s) save();
    const c = e.target.closest('[data-cancel]');
    if (c) hide(true);
  });
  window.addEventListener('resize', place);
  document.addEventListener('scroll', place, true);
  document.addEventListener('mousedown', (e) => {
    if (cur && !el.contains(e.target) && !(cur.keepOn && cur.keepOn(e.target))) hide(true);
  }, true);
  return el;
}

function setKind(k) {
  if (!cur || cur.mode === 'reply') return;
  cur.kind = k;
  el.querySelectorAll('[data-kind]').forEach((b) => b.classList.toggle('on', b.dataset.kind === k));
}

function place() {
  if (!cur || !el || el.hidden) return;
  const r = cur.getRect();
  if (!r) return;
  const w = el.offsetWidth, h = el.offsetHeight;
  const vw = document.documentElement.clientWidth, vh = window.innerHeight;
  let left = Math.min(Math.max(8, r.left - 12), vw - w - 8);
  let top = r.bottom + 8;
  if (top + h > vh - 8) top = Math.max(8, r.top - h - 8);
  el.style.left = left + 'px';
  el.style.top = top + 'px';
}

async function save() {
  if (!cur || cur.saving) return;
  const input = el.querySelector('.pop-input');
  cur.saving = true;
  input.disabled = true;
  try {
    const ok = await cur.onSave(cur.kind, input.value.trim());
    if (ok !== false) hide(false);
  } finally {
    if (cur) { cur.saving = false; input.disabled = false; }
  }
}

export function hide(cancelled = true) {
  if (!cur) return;
  const c = cur;
  cur = null;
  el.hidden = true;
  if (cancelled && c.onCancel) c.onCancel();
  if (c.onClose) c.onClose();
}

export const isOpen = () => !!cur;
export const mode = () => cur && cur.mode;

// opts: { mode:'new'|'edit'|'reply', kind, note, heading, getRect, onSave(kind,note), onCancel, onClose, focus, keepOn }
export function show(opts) {
  ensure();
  if (cur) hide(true);
  cur = { kind: 'highlight', ...opts };
  const showKinds = cur.mode !== 'reply' && cur.kind !== 'correction';
  el.innerHTML = `${cur.heading ? `<div class="pop-head">${esc(cur.heading)}</div>` : ''}
    ${showKinds ? `<div class="pop-kinds" role="radiogroup" aria-label="标注类型">${KINDS.map(([k, l], i) => `<button type="button" class="kind-btn k-${k}" data-kind="${k}" role="radio" title="${l}（${i + 1}）">${l}<span class="key">${i + 1}</span></button>`).join('')}</div>` : ''}
    <textarea class="input pop-input" rows="${(cur.note || '').includes('\n') ? 4 : 2}" placeholder="${cur.mode === 'reply' ? '回复…' : '边注（可留空，只做高亮）'}" aria-label="边注">${esc(cur.note || '')}</textarea>
    <div class="pop-hint"><span>↵ 保存 · ⇧↵ 换行 · 支持 Markdown</span><span class="sp"></span><button type="button" class="btn btn-ghost btn-xs" data-cancel>取消</button><button type="button" class="btn btn-primary btn-xs" data-save>保存</button></div>`;
  el.hidden = false;
  setKind(cur.kind);
  const input = el.querySelector('.pop-input');
  input.addEventListener('keydown', (e) => {
    if (e.isComposing || e.keyCode === 229) return;
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); save(); }
    else if (e.key === 'Escape') { e.preventDefault(); hide(true); }
    else if (/^[1-4]$/.test(e.key) && (e.altKey || !input.value) && cur.mode !== 'reply' && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      setKind(KINDS[+e.key - 1][0]);
    }
  });
  place();
  if (cur.focus) input.focus();
}

// 浮层打开、焦点仍在正文时的按键：1-4 选类型并转到输入框，↵ 直接保存，Esc 取消，其他键关闭浮层继续输入。
export function handleEditorKey(e) {
  if (!cur || cur.mode !== 'new') return false;
  if (e.isComposing || e.keyCode === 229) { hide(true); return false; }
  if (e.metaKey || e.ctrlKey || e.altKey) return false;
  if (/^[1-4]$/.test(e.key)) {
    e.preventDefault();
    setKind(KINDS[+e.key - 1][0]);
    el.querySelector('.pop-input').focus();
    return true;
  }
  if (e.key === 'Enter') { e.preventDefault(); save(); return true; }
  if (e.key === 'Escape') { e.preventDefault(); hide(true); return true; }
  if (e.key === 'Shift' || (e.shiftKey && e.key.startsWith('Arrow'))) return false;
  hide(true);
  return false;
}
