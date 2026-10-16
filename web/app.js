// EssayPilot v2 前端主模块：状态、列表抽屉、批阅视图、版本视图、SSE、底部 Agent 栏。

import * as API from './api.js';
import { esc, icon, debounce, fmtTime, fmtScore, SUBJECT_LABEL, shortDim, authorName, isAgent, toast, errToast, openDialog, confirmDialog, tagInput, compressImage, copyText, md, mdLine } from './util.js';
import { Editor, renderReading, readingSelection, paragraphsOf, paragraphIndexAt } from './editor.js';
import { layoutSide, belowNotesHTML, sideOf } from './margin.js';
import * as pop from './popover.js';
import { renderVersions, setPick } from './versions.js';
import { shiftAnnotations } from './diff.js';

const $ = (s, r = document) => r.querySelector(s);
const store = {
  get(k, d) { try { return localStorage.getItem('ep:' + k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem('ep:' + k, v); } catch { /* ignore */ } },
};

const S = {
  meta: null,
  list: [],
  listQ: '',
  listTag: '',
  collapsed: new Set(),
  id: null,
  detail: null,
  serverContent: '',
  lastValue: '',
  anns: [],
  view: 'review',
  expandAll: store.get('expandAll', '0') === '1',
  editing: false,
  hotId: null,
  hotFrom: null,
  pinnedId: null,
  caretId: null,
  expanded: new Set(),
  pending: null,
  lastSel: null,
  promptOpen: false,
  pen: false, // 荧光笔模式：在正文和题目上划选即上色，再划一次擦掉
  scoreOpen: false,
  activity: [],
  ui: null,
  saveState: '',
  pendingPin: null,
  hl: { items: [], loaded: false, q: '', subj: '', group: store.get('hlGroup', 'category'), collapsed: new Set() },
};

const narrowMQ = window.matchMedia('(max-width: 899px)');
// 窄屏放不下页边：边注落到段后。
const isBelow = () => narrowMQ.matches;

// ---------------------------------------------------------------- 派生数据

function derive() {
  const text = editor.value;
  const live = S.anns.filter((a) => a.status === 'open' && !a.parentId && a.start < a.end && a.end <= text.length);
  const marks = live.filter(isMark); // 手动荧光笔：只上色，不进页边、不编号
  const vis = live.filter((a) => !isMark(a));
  vis.sort((a, b) => a.start - b.start || a.end - b.end || (a.createdAt < b.createdAt ? -1 : 1));
  const numbers = new Map(vis.map((a, i) => [a.id, i + 1]));
  const replies = new Map();
  for (const r of S.anns) {
    if (!r.parentId) continue;
    if (!replies.has(r.parentId)) replies.set(r.parentId, []);
    replies.get(r.parentId).push(r);
  }
  for (const l of replies.values()) l.sort((a, b) => (a.createdAt < b.createdAt ? -1 : 1));
  const orphans = S.anns.filter((a) => a.status === 'orphaned' && !a.parentId);
  return { text, vis, marks, numbers, replies, orphans };
}

function isMark(a) { return a.kind === 'mark'; }
function markIdSet() { return new Set(S.anns.filter(isMark).map((a) => a.id)); }

function openId() {
  return S.pinnedId || S.hotId || S.caretId;
}

// ---------------------------------------------------------------- 编辑器

let editor;
const saveSoon = debounce(() => saveContent(), 800);
const putUISoon = debounce((c) => API.put('/ui-context', c).then((u) => { S.ui = u; renderSees(); }).catch(() => {}), 400);
let renderQueued = false;

function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => { renderQueued = false; renderMarks(); });
}

function setEditorValue(v) {
  editor.setValue(v);
  S.lastValue = v;
}

function isDirty() {
  return !!S.detail && editor.value !== S.serverContent;
}

function setSaveState(s) {
  S.saveState = s;
  const el = $('#save-state');
  if (el) el.textContent = s === 'saving' ? '保存中…' : s === 'dirty' ? '未保存' : s === 'saved' ? '已保存' : s === 'error' ? '保存失败' : '';
  if (el) el.className = 'save-state ' + s;
}

let saveChain = Promise.resolve();
function saveContent() {
  saveSoon.cancel();
  saveChain = saveChain.then(doSave).catch((e) => { setSaveState('error'); errToast(e); });
  return saveChain;
}

async function doSave() {
  if (!S.detail) return;
  const id = S.id;
  const sent = editor.value;
  if (sent === S.serverContent) return;
  setSaveState('saving');
  const d = await API.patch(`/essays/${id}`, { content: sent });
  if (S.id !== id) return;
  S.serverContent = d.essay.content;
  applyDetail(d);
  setSaveState(isDirty() ? 'dirty' : 'saved');
}

function applyDetail(d) {
  const switching = !S.detail || S.detail.essay.id !== d.essay.id;
  S.detail = d;
  if (switching) {
    S.id = d.essay.id;
    S.serverContent = d.essay.content;
    setEditorValue(d.essay.content);
    S.anns = d.annotations;
    S.pinnedId = S.hotId = S.caretId = null;
    if (S.pendingPin && d.annotations.some((a) => a.id === S.pendingPin)) S.pinnedId = S.pendingPin;
    S.expanded.clear();
    S.promptOpen = false;
    renderList(); // 点击时 S.id 还没换，加载完再刷新一次侧栏的当前项
  } else {
    const local = editor.value;
    if (local === d.essay.content || local === S.serverContent) {
      setEditorValue(d.essay.content);
      S.anns = d.annotations;
    } else {
      // 本地还有未保存输入：保留输入，把服务端标注平移到本地正文上。
      S.anns = shiftAnnotations(d.annotations, d.essay.content, local);
    }
    S.serverContent = d.essay.content;
  }
  renderAll();
}

async function loadEssay(id, { quiet } = {}) {
  try {
    const d = await API.get(`/essays/${encodeURIComponent(id)}`);
    if (S.id && S.id !== id && isDirty()) await saveContent();
    applyDetail(d);
    if (S.view !== 'highlights') $('#empty').hidden = true;
    revealPinned();
    reportUI();
    window.dispatchEvent(new CustomEvent('essaypilot:change', { detail: { id } }));
  } catch (e) {
    if (e.status === 404) {
      if (!quiet) toast('作文不存在或已删除', 'err');
      closeEssay();
    } else errToast(e);
  }
}

async function reloadCurrent() {
  if (!S.id) return;
  const id = S.id;
  if (isDirty()) await saveContent();
  try {
    const d = await API.get(`/essays/${encodeURIComponent(id)}`);
    if (S.id === id) applyDetail(d);
  } catch (e) {
    if (e.status === 404) closeEssay();
    else console.warn(e);
  }
}
const reloadSoon = debounce(reloadCurrent, 200);

function closeEssay() {
  S.id = null;
  S.detail = null;
  S.anns = [];
  setEditorValue('');
  S.serverContent = '';
  if (location.hash && S.view !== 'highlights') history.replaceState(null, '', location.pathname + location.search);
  renderList();
  renderAll();
  window.dispatchEvent(new CustomEvent('essaypilot:change', { detail: { id: null } }));
}

function onInput(v) {
  S.anns = shiftAnnotations(S.anns, S.lastValue, v);
  S.lastValue = v;
  if (pop.isOpen() && pop.mode() === 'new') pop.hide(true);
  setSaveState('dirty');
  renderWordCount();
  scheduleRender();
  saveSoon();
}

function onSelection(sel) {
  if (!S.detail) return;
  if (sel.end > sel.start) S.lastSel = { start: sel.start, end: sel.end };
  // 光标所在的标注（最短的那条）
  let best = null;
  if (document.activeElement === editor.ta) {
    for (const a of S.anns) {
      if (a.status !== 'open' || a.parentId || a.start >= a.end || isMark(a)) continue;
      if (sel.start >= a.start && sel.start <= a.end && (!best || a.end - a.start < best.end - best.start)) best = a;
    }
  }
  const nid = best ? best.id : null;
  if (nid !== S.caretId) {
    S.caretId = nid;
    if (!isBelow()) layoutMargins();
  }
  reportUI(sel);
}

function reportUI(sel) {
  if (!S.id || S.view === 'highlights') { putUISoon({ essayId: '', selectionStart: 0, selectionEnd: 0, selectionText: '' }); return; }
  const s = sel || editor.selection();
  putUISoon({ essayId: S.id, selectionStart: s.start, selectionEnd: s.end, selectionText: editor.value.slice(s.start, s.end) });
}

function setHot(id, from) {
  if (id === S.hotId) return;
  S.hotId = id;
  S.hotFrom = from;
  editor.setHot(id);
  const r = $('#reading');
  r.querySelectorAll('.hot').forEach((el) => el.classList.remove('hot'));
  if (id) r.querySelectorAll(`[data-id="${CSS.escape(id)}"]`).forEach((el) => el.classList.add('hot'));
  if (!isBelow()) layoutMargins(from === 'margin');
}

// ---------------------------------------------------------------- 标注浮层

function requestAnnotate(range, getRect) {
  if (!S.detail) return;
  S.pending = range;
  scheduleRender();
  pop.show({
    mode: 'new',
    kind: 'highlight',
    getRect,
    focus: false,
    onSave: async (kind, note) => {
      try {
        if (isDirty()) await saveContent();
        const res = await API.post(`/essays/${S.id}/annotations`, { items: [{ start: range.start, end: range.end, kind, note }] });
        const r = res && res[0];
        if (r && r.error) { toast(r.error, 'err'); return false; }
        await reloadCurrent();
        return true;
      } catch (e) { errToast(e); return false; }
    },
    onClose: () => { S.pending = null; scheduleRender(); },
  });
}

function noteRect(id) {
  const el = document.querySelector(`.note[data-id="${CSS.escape(id)}"]`) || document.querySelector(`[data-id="${CSS.escape(id)}"]`);
  return el ? el.getBoundingClientRect() : null;
}

function findAnn(id) {
  return S.anns.find((a) => a.id === id);
}

async function act(name, el) {
  const id = el.dataset.id;
  try {
    switch (name) {
      case 'apply': {
        if (isDirty()) await saveContent();
        const d = await API.post(`/annotations/${id}/apply`);
        applyDetail(d);
        toast('已采纳');
        break;
      }
      case 'apply-all': {
        if (isDirty()) await saveContent();
        const ids = (el.dataset.ids || '').split(',').filter(Boolean);
        const d = await API.post(`/essays/${S.id}/apply`, ids.length ? { ids } : {});
        applyDetail(d);
        toast(`已采纳 ${ids.length || ''} 条`.replace('  ', ' '));
        break;
      }
      case 'reply': {
        const a = findAnn(id);
        S.pinnedId = id;
        pop.show({
          mode: 'reply', heading: '回复', focus: true,
          getRect: () => noteRect(id),
          onSave: async (_k, note) => {
            if (!note) return false;
            const res = await API.post(`/essays/${S.id}/annotations`, { items: [{ parentId: id, kind: a ? a.kind : 'question', note }] });
            if (res && res[0] && res[0].error) { toast(res[0].error, 'err'); return false; }
            await reloadCurrent();
            return true;
          },
        });
        break;
      }
      case 'edit': {
        const a = findAnn(id);
        if (!a) return;
        pop.show({
          mode: 'edit', kind: a.kind, note: a.note, heading: '编辑边注', focus: true,
          getRect: () => noteRect(id),
          onSave: async (kind, note) => {
            const body = { note };
            if (kind !== a.kind) body.kind = kind;
            await API.patch(`/annotations/${id}`, body);
            await reloadCurrent();
            return true;
          },
        });
        break;
      }
      case 'resolve':
        await API.patch(`/annotations/${id}`, { status: 'resolved' });
        if (S.pinnedId === id) S.pinnedId = null;
        await reloadCurrent();
        break;
      case 'delete':
        await API.del(`/annotations/${id}`);
        if (S.pinnedId === id) S.pinnedId = null;
        await reloadCurrent();
        break;
      case 'relocate': {
        const sel = S.lastSel;
        if (!sel || sel.end <= sel.start) { toast('先在正文中选中新位置，再点“重新定位”'); return; }
        if (isDirty()) await saveContent();
        await API.patch(`/annotations/${id}`, { start: sel.start, end: sel.end });
        await reloadCurrent();
        toast('已重新定位');
        break;
      }
      case 'expand': {
        const k = el.dataset.key;
        if (S.expanded.has(k)) S.expanded.delete(k);
        else S.expanded.add(k);
        layoutMargins();
        break;
      }
      case 'adopt-polished': {
        const ev = S.detail.evaluations[0];
        if (!ev || !ev.polished) return;
        if (!(await confirmDialog('采纳润色稿', '用最新评分的润色稿替换当前正文，并保存为新版本。原文仍保留在历史版本里。', '采纳'))) return;
        saveSoon.cancel();
        const d = await API.patch(`/essays/${S.id}`, { content: ev.polished });
        S.serverContent = d.essay.content;
        setEditorValue(d.essay.content);
        applyDetail(d);
        await API.post(`/essays/${S.id}/versions`, { note: '采纳润色稿' });
        setPick(S.detail, { from: 'current', to: 'current' });
        await reloadCurrent();
        toast('已采纳润色稿为新版本');
        break;
      }
      case 'save-version': {
        const note = await askText('保存当前版本', '版本备注（可留空）', '');
        if (note === null) return;
        if (isDirty()) await saveContent();
        await API.post(`/essays/${S.id}/versions`, { note });
        await reloadCurrent();
        toast('已保存版本');
        break;
      }
      case 'restore': {
        if (!(await confirmDialog('恢复版本', '当前稿会先自动保存为“恢复前自动保存”版本，然后用所选版本覆盖标题、题型、题目与正文。', '恢复'))) return;
        if (isDirty()) await saveContent();
        const d = await API.post(`/essays/${S.id}/versions/${id}/restore`);
        S.serverContent = d.essay.content;
        setEditorValue(d.essay.content);
        applyDetail(d);
        setPick(S.detail, { from: 'current', to: 'current' });
        renderAll();
        toast('已恢复');
        break;
      }
      case 'delete-version': {
        if (!(await confirmDialog('删除版本', '删除后不可恢复；若是评分快照，对应评分也会一起删除。', '删除'))) return;
        await API.del(`/essays/${S.id}/versions/${id}`);
        await reloadCurrent();
        break;
      }
      default:
    }
  } catch (e) { errToast(e); }
}

function askText(title, label, value) {
  return new Promise((resolve) => {
    const d = openDialog(title, `<div class="field"><label>${esc(label)}</label><input class="input" value="${esc(value)}"></div>`,
      '<button class="btn btn-secondary" data-r="0">取消</button><button class="btn btn-primary" data-r="1">确定</button>');
    const input = d.el.querySelector('input');
    input.focus();
    let done = false;
    const fin = (v) => { if (done) return; done = true; d.close(); resolve(v); };
    d.el.addEventListener('cancel', () => fin(null));
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.isComposing) fin(input.value.trim()); });
    d.el.querySelector('[data-r="1"]').addEventListener('click', () => fin(input.value.trim()));
    d.el.querySelector('[data-r="0"]').addEventListener('click', () => fin(null));
  });
}

// ---------------------------------------------------------------- 渲染

function typeSpec(id) {
  return S.meta.types.find((t) => t.id === id);
}

function renderAll() {
  const d = S.detail;
  const hl = S.view === 'highlights';
  document.body.classList.toggle('no-essay', !d || hl);
  $('#empty').hidden = !!d || hl;
  $('#highlights').hidden = !hl;
  $('#btn-highlights').classList.toggle('on', hl);
  renderTop();
  if (hl) {
    $('#review').hidden = true;
    $('#versions').hidden = true;
    renderHighlights();
    renderSees();
    return;
  }
  if (!d) {
    $('#review').hidden = true;
    $('#versions').hidden = true;
    renderSees();
    return;
  }
  $('#review').hidden = S.view !== 'review';
  $('#versions').hidden = S.view !== 'versions';
  renderScorebar();
  renderInfo();
  const t = $('#title');
  if (document.activeElement !== t) { t.value = d.essay.title; fitTitle(); }
  renderWordCount();
  renderMarks();
  if (S.view === 'versions') renderVersionsView();
  renderSees();
}

function renderTop() {
  const d = S.detail;
  const crumbs = $('#crumbs');
  if (S.view === 'highlights') {
    crumbs.innerHTML = '<span class="brand">EssayPilot</span><span class="crumb-sub">/ 重点句</span>';
    $('#top-score').innerHTML = '';
  } else if (!d) {
    crumbs.innerHTML = '<span class="brand">EssayPilot</span>';
    $('#top-score').innerHTML = '';
  } else {
    const spec = typeSpec(d.essay.type);
    crumbs.innerHTML = `<span class="crumb-sub">${esc(SUBJECT_LABEL[d.essay.subject] || d.essay.subject)} · ${esc(spec ? spec.label : d.essay.type)} /</span><span class="crumb-title">${esc(d.essay.title || '未命名')}</span>`;
    const ev = d.evaluations[0];
    $('#top-score').innerHTML = ev ? `${fmtScore(ev.score)}<span class="of">/${fmtScore(ev.maxScore)}</span>` : '';
  }
  document.querySelectorAll('[data-view]').forEach((b) => {
    b.classList.toggle('on', b.dataset.view === S.view);
    b.setAttribute('aria-selected', b.dataset.view === S.view);
    b.disabled = !d;
  });
  document.title = S.view === 'highlights' ? '重点句 · EssayPilot' : d ? `${d.essay.title || '未命名'} · EssayPilot` : 'EssayPilot';
}

function renderScorebar() {
  const d = S.detail;
  const ev = d.evaluations[0];
  const el = $('#scorebar');
  if (!ev) {
    el.innerHTML = `<div class="sb-none"><span class="sb-big muted">—</span><span class="muted small">尚未评分。服务端不调模型：用底部「复制 grade_essay」把提示交给本地 Agent，评分会实时出现在这里。</span></div>`;
    return;
  }
  const dims = S.meta.dimensions[d.essay.subject] || {};
  const bars = (S.meta.dimensionKeys || Object.keys(ev.dimensions)).map((k) => {
    const x = ev.dimensions[k];
    if (!x) return '';
    const pct = x.maxScore ? Math.max(0, Math.min(100, (x.score / x.maxScore) * 100)) : 0;
    return `<div class="dim" title="${esc(dims[k]?.label || k)}：${esc(x.feedback || '')}"><span>${esc(shortDim(dims[k]?.label || k))} ${fmtScore(x.score)}/${fmtScore(x.maxScore)}</span><span class="bar"><span style="width:${pct.toFixed(0)}%"></span></span></div>`;
  }).join('');
  el.innerHTML = `<button class="sb-row" id="sb-toggle" aria-expanded="${S.scoreOpen}">
      <span class="sb-big">${fmtScore(ev.score)}<span class="of">/${fmtScore(ev.maxScore)}</span></span>
      ${ev.band ? `<span class="tag tag-accent">${esc(ev.band)}</span>` : ''}
      <span class="dims">${bars}</span><span class="sp"></span>
      <span class="sb-more">总评 · 亮点 · 短板 ${S.scoreOpen ? '▴' : '▾'}</span></button>
    ${S.scoreOpen ? `<div class="sb-detail">
      <div><h4>总评</h4><p>${esc(ev.overallComment || '—')}</p><p class="muted small">${esc(authorName(ev.author))} · ${fmtTime(ev.createdAt)} · <button class="lnk" data-goto="versions">在版本页对照润色稿 →</button></p></div>
      <div><h4>亮点</h4><ul>${(ev.strengths || []).map((s) => `<li>${esc(s)}</li>`).join('') || '<li class="muted">—</li>'}</ul></div>
      <div><h4>短板</h4><ul>${(ev.weaknesses || []).map((s) => `<li>${esc(s)}</li>`).join('') || '<li class="muted">—</li>'}</ul></div>
    </div>` : ''}`;
}

function renderWordCount() {
  const d = S.detail;
  if (!d) return;
  const spec = typeSpec(d.essay.type);
  const text = editor.value;
  let n;
  if (d.essay.subject === 'english') n = (text.match(/[a-zA-Z0-9'-]+/g) || []).length;
  else n = (text.match(/[一-龥]/g) || []).length + (text.replace(/[一-龥]/g, ' ').match(/[a-zA-Z0-9'-]+/g) || []).length;
  const unit = d.essay.subject === 'english' ? '词' : '字';
  const out = spec && (n < spec.minLength || n > spec.maxLength) && n > 0;
  $('#wc').innerHTML = `<span class="${out ? 'warn' : ''}">${n} ${unit}</span>${spec ? ` · 要求 ${spec.minLength}–${spec.maxLength}` : ''} <span id="save-state" class="save-state ${S.saveState}"></span>`;
  setSaveState(S.saveState);
}

// 标题可能很长：textarea 随内容增高。
function fitTitle() {
  const t = $('#title');
  t.style.height = 'auto';
  t.style.height = t.scrollHeight + 'px';
}

// 标题下方的题目卡：题型、分类、标签与题目要求；点击弹出编辑框。
function renderInfo() {
  const e = S.detail.essay;
  const spec = typeSpec(e.type);
  const box = $('#info');
  const prompt = e.prompt.trim();
  box.innerHTML = `<div class="pinfo-meta"><span>${esc(SUBJECT_LABEL[e.subject])} · ${esc(spec ? spec.label : e.type)}${e.category ? ' · ' + esc(e.category) : ''}</span>
      ${e.tags.map((t) => `<span class="tagref">#${esc(t)}</span>`).join(' ')}<span class="sp"></span><span class="pinfo-edit">${icon('pen', 13)} 编辑</span></div>
    <div class="pinfo-text${prompt ? '' : ' empty'}${S.promptOpen ? ' open' : ''}">${prompt ? promptHTML(e.prompt) : '还没有题目要求，点击添加'}</div>
    <button class="lnk pinfo-more" hidden></button>
    ${e.promptImage ? `<img class="plate pinfo-img" src="${esc(e.promptImage)}" alt="题目图片">` : ''}`;
  const text = box.querySelector('.pinfo-text');
  const more = box.querySelector('.pinfo-more');
  const clipped = text.scrollHeight > text.clientHeight + 2;
  if (S.promptOpen || clipped) {
    more.hidden = false;
    more.textContent = S.promptOpen ? '收起 ▴' : '展开全文 ▾';
    text.classList.toggle('clipped', clipped && !S.promptOpen);
  }
}

// ---------------------------------------------------------------- 荧光笔（手动分点）
// 正文上的标记存成 kind=mark 的标注；题目上的标记直接写进题目文本，用 ==…== 包住。

const PROMPT_MARK_RE = /==([\s\S]+?)==/g;

// 题目里 ==…== 的位置：[a,b) 含定界符，[s,e) 是被标记的文字。
function promptMarks(p) {
  return [...p.matchAll(PROMPT_MARK_RE)].map((m) => ({ a: m.index, b: m.index + m[0].length, s: m.index + 2, e: m.index + m[0].length - 2 }));
}

// 每段文字包 data-o（原始题目文本里的偏移），划选时换算回去。
function promptHTML(raw) {
  const from = raw.length - raw.trimStart().length, to = raw.trimEnd().length;
  const span = (x, y) => (x < y ? `<span data-o="${x}">${esc(raw.slice(x, y))}</span>` : '');
  let html = '', pos = from;
  for (const m of promptMarks(raw)) {
    if (m.b <= from || m.a >= to) continue;
    html += span(pos, m.a) + `<mark class="m k-mark" data-pm="${m.a}">${span(m.s, m.e)}</mark>`;
    pos = m.b;
  }
  return html + span(pos, to);
}

function promptOffset(node, off) {
  const el = (node.nodeType === 3 ? node.parentElement : node)?.closest('[data-o]');
  if (!el) return null;
  return Number(el.dataset.o) + (node.nodeType === 3 ? off : 0);
}

async function penPrompt(ev) {
  const raw = S.detail.essay.prompt;
  const marks = promptMarks(raw);
  const sel = window.getSelection();
  let s, e;
  if (sel && !sel.isCollapsed && sel.rangeCount) {
    const r = sel.getRangeAt(0);
    s = promptOffset(r.startContainer, r.startOffset);
    e = promptOffset(r.endContainer, r.endOffset);
    sel.removeAllRanges();
  } else {
    const m = ev.target.closest('[data-pm]');
    if (!m) return;
    s = e = Number(m.dataset.pm) + 2;
  }
  if (s == null || e == null) return;
  const hit = marks.filter((m) => (s === e ? s >= m.s && s <= m.e : s < m.b && e > m.a));
  let next;
  if (hit.length) {
    next = raw;
    for (const m of hit.reverse()) next = next.slice(0, m.a) + next.slice(m.s, m.e) + next.slice(m.b);
  } else {
    while (s < e && /\s/.test(raw[s])) s++;
    while (e > s && /\s/.test(raw[e - 1])) e--;
    if (s >= e) return;
    next = raw.slice(0, s) + '==' + raw.slice(s, e) + '==' + raw.slice(e);
  }
  try { applyDetail(await API.patch(`/essays/${S.id}`, { prompt: next })); } catch (err) { errToast(err); }
}

async function penContent(sel) {
  if (!S.detail) return;
  const marks = S.anns.filter((a) => isMark(a) && a.status === 'open' && !a.parentId);
  const hit = marks.filter((a) => (sel.start === sel.end ? sel.start > a.start && sel.start < a.end : sel.start < a.end && sel.end > a.start));
  try {
    if (hit.length) {
      await Promise.all(hit.map((a) => API.del(`/annotations/${a.id}`)));
    } else {
      const text = editor.value;
      let { start, end } = sel;
      while (start < end && /\s/.test(text[start])) start++;
      while (end > start && /\s/.test(text[end - 1])) end--;
      if (start >= end) return;
      if (isDirty()) await saveContent();
      const res = await API.post(`/essays/${S.id}/annotations`, { items: [{ start, end, kind: 'mark' }] });
      if (res && res[0] && res[0].error) { toast(res[0].error, 'err'); return; }
    }
    await reloadCurrent();
  } catch (e) { errToast(e); }
}

function setPen(on) {
  S.pen = on;
  if (on) S.promptOpen = true;
  document.body.classList.toggle('pen', on);
  const b = $('#pen-toggle');
  b.classList.toggle('on', on);
  b.setAttribute('aria-pressed', on);
  if (S.detail) renderInfo();
}

function editInfoDialog() {
  const e = S.detail.essay;
  const types = S.meta.types.filter((t) => t.subject === e.subject);
  let image = e.promptImage || '';
  const d = openDialog('编辑题目信息', `<form class="new-form" id="info-form">
      <div class="info-grid">
        <div class="field"><label for="f-type">题型</label><select id="f-type" class="input">${types.map((t) => `<option value="${t.id}"${t.id === e.type ? ' selected' : ''}>${esc(t.label)}</option>`).join('')}</select></div>
        <div class="field" id="f-cat-f"><label for="f-cat">分类</label><select id="f-cat" class="input"></select></div>
        <div class="field wide"><label>主题标签（至少一个）</label><div id="f-tags"></div></div>
        <div class="field wide"><label for="f-prompt">题目要求</label><textarea id="f-prompt" class="input prompt-ta" rows="10" placeholder="粘贴题目要求；也可以直接粘贴题目图片">${esc(e.prompt)}</textarea></div>
        <div class="field wide"><label>题目图片</label><div class="img-row" id="f-img-row"></div></div>
      </div>
      <div class="form-err" id="f-err"></div></form>`,
  '<button class="btn btn-secondary" data-r="0">取消</button><button class="btn btn-primary" data-r="1">保存</button>');
  const el = d.el;
  el.querySelector('.dialog').classList.add('dialog-wide');
  const fillCats = (keep) => {
    const cats = typeSpec(el.querySelector('#f-type').value)?.categories || [];
    el.querySelector('#f-cat-f').hidden = !cats.length;
    el.querySelector('#f-cat').innerHTML = (cats.includes(keep) ? '' : '<option value="">请选择</option>') + cats.map((c) => `<option${c === keep ? ' selected' : ''}>${esc(c)}</option>`).join('');
  };
  const renderImg = () => {
    el.querySelector('#f-img-row').innerHTML = `${image ? `<img class="plate prompt-img" src="${esc(image)}" alt="题目图片">` : '<span class="muted small">无</span>'}
      <label class="btn btn-secondary btn-sm">${icon('image', 14)} 选择图片<input type="file" accept="image/*" id="f-img" hidden></label>
      ${image ? '<button type="button" class="btn btn-ghost btn-sm" id="f-img-del">删除图片</button>' : ''}<span class="muted xsmall">也可在题目要求框内粘贴</span>`;
  };
  const setImage = async (f) => {
    try { image = await compressImage(f); renderImg(); } catch (err) { errToast(err); }
  };
  fillCats(e.category);
  renderImg();
  const tags = tagInput(el.querySelector('#f-tags'), e.tags, null);
  el.querySelector('#f-type').addEventListener('change', () => fillCats(el.querySelector('#f-cat').value || e.category));
  el.querySelector('#f-img-row').addEventListener('change', (ev) => { if (ev.target.id === 'f-img' && ev.target.files[0]) setImage(ev.target.files[0]); });
  el.querySelector('#f-img-row').addEventListener('click', (ev) => { if (ev.target.closest('#f-img-del')) { image = ''; renderImg(); } });
  const pr = el.querySelector('#f-prompt');
  pr.addEventListener('paste', (ev) => {
    const f = [...(ev.clipboardData?.files || [])].find((x) => x.type.startsWith('image/'));
    if (!f) return;
    ev.preventDefault();
    setImage(f);
  });
  el.querySelector('[data-r="0"]').addEventListener('click', d.close);
  el.querySelector('[data-r="1"]').addEventListener('click', async () => {
    const err = el.querySelector('#f-err');
    const type = el.querySelector('#f-type').value;
    const cats = typeSpec(type)?.categories || [];
    const category = cats.length ? el.querySelector('#f-cat').value : '';
    const t = tags.get();
    if (cats.length && !category) { err.textContent = '请选择分类'; return; }
    if (!t.length) { err.textContent = '请至少保留一个主题标签'; return; }
    const body = {};
    if (type !== e.type) body.type = type;
    if (category !== e.category) body.category = category;
    if (t.join('\n') !== e.tags.join('\n')) body.tags = t;
    if (pr.value !== e.prompt) body.prompt = pr.value;
    if (image !== (e.promptImage || '')) body.promptImage = image;
    if (!Object.keys(body).length) { d.close(); return; }
    try {
      applyDetail(await API.patch(`/essays/${S.id}`, body));
      refreshListSoon();
      d.close();
      toast('已保存题目信息');
    } catch (ex) { err.textContent = ex.message; }
  });
  pr.focus();
}

let prevTops = new Map();

function renderMarks() {
  if (!S.detail) return;
  const D = derive();
  const below = isBelow();
  const desk = $('#desk');
  desk.classList.toggle('below', below);
  const ea = $('#expand-all');
  ea.classList.toggle('on', S.expandAll);
  ea.setAttribute('aria-pressed', S.expandAll);
  const showEditor = !below || S.editing;
  $('#editor').hidden = !showEditor;
  $('#reading').hidden = showEditor;
  const et = $('#edit-toggle');
  et.hidden = !below;
  et.innerHTML = S.editing ? `${icon('check', 14)} 完成编辑` : `${icon('pen', 14)} 编辑正文`;

  editor.render({ anns: D.vis.concat(D.marks), numbers: showEditor && !below ? D.numbers : new Map(), pending: S.pending, hotId: S.hotId });
  S._D = D;
  if (!below) layoutMargins();
  else {
    $('#mL').innerHTML = '';
    $('#mR').innerHTML = '';
    if (!showEditor) {
      const groups = groupByPara(D, paragraphsOf(D.text), null);
      renderReading($('#reading'), D.text, D.vis.concat(D.marks), D.numbers, (p) => belowNotesHTML(groups.get(p.i), ctxFor(D)), S.pending);
    }
  }
  renderOrphans(D);
}

function groupByPara(D, paras, side) {
  const groups = new Map();
  for (const a of D.vis) {
    if (side && sideOf(a) !== side) continue;
    const i = paragraphIndexAt(paras, a.start);
    if (!groups.has(i)) groups.set(i, []);
    groups.get(i).push(a);
  }
  return groups;
}

function ctxFor(D) {
  return { numbers: D.numbers, replies: D.replies, openId: openId(), hotId: S.hotId, expanded: S.expanded, expandAll: S.expandAll, markTop: (id) => editor.markTop(id) };
}

function layoutMargins(freeze = false) {
  if (!S.detail || isBelow()) return;
  const D = S._D || derive();
  const geo = editor.geometry();
  const paras = editor.paras || paragraphsOf(D.text);
  const ctx = ctxFor(D);
  const keepTops = freeze ? new Map(prevTops) : null;
  for (const [side, el] of [['L', $('#mL')], ['R', $('#mR')]]) {
    layoutSide(el, side, groupByPara(D, paras, side), geo, ctx);
    if (keepTops) {
      let prevBottom = -Infinity;
      for (const cl of el.querySelectorAll('.cluster')) {
        const t = keepTops.get(cl.dataset.key + side);
        if (t !== undefined) {
          const top = Math.max(t, prevBottom + 6);
          cl.style.top = top + 'px';
          prevBottom = top + cl.offsetHeight;
        }
      }
    }
  }
  prevTops = new Map();
  for (const [side, el] of [['L', $('#mL')], ['R', $('#mR')]]) {
    for (const cl of el.querySelectorAll('.cluster')) prevTops.set(cl.dataset.key + side, parseFloat(cl.style.top));
  }
  const n = D.vis.length;
  const nl = D.vis.filter((a) => sideOf(a) === 'L').length;
  $('#head-l').textContent = `重点 ${nl || ''}`;
  $('#head-r').textContent = `问题 · 纠错 ${n - nl || ''}`;
}

function renderOrphans(D) {
  const el = $('#orphans');
  if (!D.orphans.length) { el.innerHTML = ''; return; }
  el.innerHTML = `<div class="orph-h">孤立标注 ${D.orphans.length}</div>` + D.orphans.map((a) => `<div class="orph">
    <span class="${isAgent(a.author) ? 'ag' : 'us'}">“${esc(a.quote.length > 50 ? a.quote.slice(0, 50) + '…' : a.quote)}” 在正文中找不到${a.note ? ' · ' + mdLine(a.note) : ''} <span class="muted xsmall">${esc(authorName(a.author))}</span></span>
    <button class="lnk" data-act="relocate" data-id="${esc(a.id)}" title="先在正文中选中新位置">重新定位</button><button class="lnk" data-act="delete" data-id="${esc(a.id)}">删除</button></div>`).join('');
}

function renderVersionsView() {
  if (!S.detail) return;
  renderVersions($('#versions'), { detail: S.detail, meta: S.meta, content: editor.value, dirty: isDirty() });
}

// ---------------------------------------------------------------- 底部 Agent 栏

function renderActivity() {
  const el = $('#activity');
  if (!S.activity.length) { el.innerHTML = '<div class="muted small">还没有 Agent 调用。通过 MCP 或 WebMCP 接入后，调用会实时列在这里。</div>'; return; }
  el.innerHTML = S.activity.slice(0, 60).map((a) => `<div class="act${a.essayId && a.essayId === S.id ? ' here' : ''}"><span class="t">${fmtTime(a.at)}</span><span class="tool">${esc(a.tool)}</span><span class="sum">${esc(a.summary)} <span class="muted xsmall">${esc(authorName(a.actor))}</span></span></div>`).join('');
}

function renderSees() {
  const el = $('#sees');
  const u = S.ui;
  if (S.view === 'highlights') { el.textContent = '重点句页（未打开作文）'; return; }
  if (!u || !u.essayId) { el.textContent = S.id ? '本篇 · 同步中…' : '未打开作文'; return; }
  if (u.essayId !== S.id) { el.textContent = '另一篇作文（' + u.essayId + '）'; return; }
  const paras = paragraphsOf(editor.value);
  const pi = paragraphIndexAt(paras, u.selectionStart) + 1;
  if (u.selectionEnd > u.selectionStart) {
    const t = u.selectionText || '';
    el.innerHTML = `本篇 · 选中第 ${pi} 段 “${esc(t.length > 28 ? t.slice(0, 28) + '…' : t)}”（${t.length} 字符）`;
  } else el.textContent = `本篇 · 光标在第 ${pi} 段`;
}

async function copyPrompt(name) {
  if (!S.id) return;
  try {
    if (isDirty()) await saveContent();
    const r = await API.get(`/prompts/${name}?essay=${encodeURIComponent(S.id)}`);
    await copyText(r.text);
    toast(`已复制 ${name}，粘贴给本地 Agent`);
  } catch (e) { errToast(e); }
}

// ---------------------------------------------------------------- 列表抽屉

const refreshListSoon = debounce(() => loadList(), 400);

async function loadList() {
  try {
    S.list = await API.get('/essays' + API.qs({ q: S.listQ }));
    renderList();
  } catch (e) { console.warn(e); }
}

function renderList() {
  const tags = new Map();
  for (const e of S.list) for (const t of e.tags || []) tags.set(t, (tags.get(t) || 0) + 1);
  $('#tag-filter').innerHTML = [...tags.entries()].sort((a, b) => b[1] - a[1]).map(([t, n]) => `<button class="tag ${S.listTag === t ? 'tag-accent' : 'tag-outline'}" data-tag="${esc(t)}">#${esc(t)} <span class="n">${n}</span></button>`).join('');
  const items = S.listTag ? S.list.filter((e) => (e.tags || []).includes(S.listTag)) : S.list;
  let html = '';
  for (const subj of ['english', 'politics']) {
    const sItems = items.filter((e) => e.subject === subj);
    if (!sItems.length) continue;
    const sk = 's:' + subj;
    const sOpen = !S.collapsed.has(sk);
    html += `<div class="tree-s"><button class="tree-h s" data-col="${sk}" aria-expanded="${sOpen}"><span class="car">${sOpen ? '▾' : '▸'}</span><span class="nm">${SUBJECT_LABEL[subj]}</span><span class="n">${sItems.length}</span></button>`;
    if (sOpen) {
      html += '<div class="tree-in">';
      for (const t of S.meta.types.filter((x) => x.subject === subj)) {
        const tItems = sItems.filter((e) => e.type === t.id);
        if (!tItems.length) continue;
        const tk = 't:' + t.id;
        const tOpen = !S.collapsed.has(tk);
        html += `<button class="tree-h t" data-col="${tk}" aria-expanded="${tOpen}"><span class="car">${tOpen ? '▾' : '▸'}</span><span class="nm">${esc(t.label)}</span><span class="n">${tItems.length}</span></button>`;
        if (tOpen) {
          html += '<div class="tree-in">' + tItems.map((e) => `<div class="tree-e${e.id === S.id ? ' cur' : ''}" data-open="${esc(e.id)}" role="button" tabindex="0">
            <span class="nm" title="${esc(e.title)}">${esc(e.title || '未命名')}</span>
            <span class="sc">${e.latestScore ? fmtScore(e.latestScore.score) : '—'}</span>
            <button class="del" data-del="${esc(e.id)}" aria-label="删除 ${esc(e.title)}">${icon('trash', 13)}</button></div>`).join('') + '</div>';
        }
      }
      html += '</div>';
    }
    html += '</div>';
  }
  $('#tree').innerHTML = html || `<div class="muted small pad">${S.listQ || S.listTag ? '没有匹配的作文' : '还没有作文，点“新建”开始。'}</div>`;
}

// 宽屏：抽屉在主区左侧占位，打开时把内容推向右侧（记住开合状态）；窄屏：覆盖层 + 遮罩。
function openDrawer(open, { focus = true } = {}) {
  const dr = $('#drawer');
  dr.classList.toggle('open', open);
  $('#drawer-back').hidden = !open || !narrowMQ.matches;
  dr.setAttribute('aria-hidden', !open);
  if (!narrowMQ.matches) store.set('drawer', open ? 'open' : 'closed');
  if (open) { loadList(); if (focus) setTimeout(() => $('#list-q').focus(), 50); }
}

// ---------------------------------------------------------------- 重点句页

const HL_GROUPS = {
  category: (h) => [h.essayCategory || '未分类'],
  type: (h) => [typeSpec(h.essayType)?.label || h.essayType],
  label: (h) => [h.label || '未标注'],
  tag: (h) => (h.essayTags.length ? h.essayTags.map((t) => '#' + t) : ['无标签']),
  essay: (h) => [h.essayTitle || '未命名'],
};

async function loadHighlights() {
  try {
    S.hl.items = await API.get('/highlights');
    S.hl.loaded = true;
    if (S.view === 'highlights') renderHighlights();
  } catch (e) { errToast(e); }
}
const loadHighlightsSoon = debounce(loadHighlights, 300);

// 转义并把搜索词包成 <mark>。
function markQuery(text, q) {
  if (!q) return esc(text);
  const lo = text.toLowerCase(), ql = q.toLowerCase();
  let out = '', i = 0, j;
  while ((j = lo.indexOf(ql, i)) >= 0) {
    out += esc(text.slice(i, j)) + '<mark>' + esc(text.slice(j, j + q.length)) + '</mark>';
    i = j + q.length;
  }
  return out + esc(text.slice(i));
}

function renderHighlights() {
  const H = S.hl;
  $('#hl-group').value = H.group;
  $('#hl-subj').querySelectorAll('[data-subj]').forEach((b) => b.classList.toggle('on', b.dataset.subj === H.subj));
  const list = $('#hl-list');
  if (!H.loaded) { list.innerHTML = '<div class="hl-empty">加载中…</div>'; return; }
  const q = H.q.toLowerCase();
  const items = H.items.filter((h) => (!H.subj || h.essaySubject === H.subj) && (!q ||
    [h.quote, h.note, h.label, h.essayTitle, h.essayCategory, ...h.essayTags].some((x) => (x || '').toLowerCase().includes(q))));
  $('#hl-count').textContent = items.length === H.items.length ? `共 ${items.length} 句` : `${items.length} / ${H.items.length} 句`;
  if (!items.length) {
    list.innerHTML = `<div class="hl-empty">${H.items.length ? '没有匹配的重点句' : '还没有重点句。在批阅页选中文字并标为“重点”，或让 Agent 自动标注。'}</div>`;
    return;
  }
  const groups = new Map();
  for (const h of items) {
    for (const k of HL_GROUPS[H.group](h)) {
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(h);
    }
  }
  const keys = [...groups.keys()];
  if (H.group !== 'essay') keys.sort((a, b) => groups.get(b).length - groups.get(a).length || a.localeCompare(b, 'zh'));
  list.innerHTML = keys.map((k) => {
    const gk = H.group + ':' + k;
    const open = !H.collapsed.has(gk);
    const rows = groups.get(k);
    return `<section class="hl-sec"><button class="hl-sec-h" data-hl-col="${esc(gk)}" aria-expanded="${open}"><span class="car">${open ? '▾' : '▸'}</span><span class="nm">${esc(k)}</span><span class="n">${rows.length}</span></button>
      ${open ? rows.map((h) => `<a class="hl-item" href="#/e/${encodeURIComponent(h.essayId)}" data-hl-open="${esc(h.essayId)}" data-aid="${esc(h.id)}">
        <div class="hl-quote">${markQuery(h.quote, H.q)}</div>
        ${h.note ? `<div class="hl-note md">${md(h.note)}</div>` : ''}
        <div class="hl-meta">${h.label && H.group !== 'label' ? `<span class="lb">${esc(h.label)}</span>` : ''}${H.group !== 'essay' ? `<span>${esc(h.essayTitle || '未命名')}</span>` : ''}<span>${esc(SUBJECT_LABEL[h.essaySubject] || h.essaySubject)} · ${esc(typeSpec(h.essayType)?.shortLabel || typeSpec(h.essayType)?.label || h.essayType)}</span><span>${esc(authorName(h.author))}</span></div>
      </a>`).join('') : ''}</section>`;
  }).join('');
}

// 从重点句页跳到作文，并展开、滚动到这条标注。
function openAnnotation(essayId, aid) {
  S.pendingPin = aid;
  if (essayId === S.id && S.detail) {
    S.pinnedId = aid;
    go(essayId, 'review');
    revealPinned();
  } else go(essayId, 'review');
}

function revealPinned() {
  const id = S.pendingPin;
  if (!id) return;
  S.pendingPin = null;
  requestAnimationFrame(() => {
    const m = document.querySelector(`#editor mark[data-id="${CSS.escape(id)}"], #reading mark[data-id="${CSS.escape(id)}"]`);
    if (m) m.scrollIntoView({ block: 'center' });
  });
}

function newEssayDialog() {
  const subjects = ['english', 'politics'];
  const d = openDialog('新建作文', `<form class="new-form" id="new-form">
      <div class="field"><label>科目</label><div class="seg" id="n-subj">${subjects.map((s, i) => `<label class="seg-opt"><input type="radio" name="subj" value="${s}"${i === 0 ? ' checked' : ''}>${SUBJECT_LABEL[s]}</label>`).join('')}</div></div>
      <div class="field"><label for="n-type">题型</label><select class="input" id="n-type"></select></div>
      <div class="field" id="n-cat-f"><label for="n-cat">分类</label><select class="input" id="n-cat"></select></div>
      <div class="field"><label>主题标签（至少一个）</label><div id="n-tags"></div></div>
      <div class="field"><label for="n-title">标题（可留空）</label><input class="input" id="n-title"></div>
      <div class="form-err" id="n-err"></div></form>`,
  '<button class="btn btn-secondary" data-r="0">取消</button><button class="btn btn-primary" data-r="1">创建</button>');
  const el = d.el;
  const subj = () => el.querySelector('input[name=subj]:checked').value;
  const fillTypes = () => {
    el.querySelector('#n-type').innerHTML = S.meta.types.filter((t) => t.subject === subj()).map((t) => `<option value="${t.id}">${esc(t.label)}</option>`).join('');
    fillCats();
  };
  const fillCats = () => {
    const cats = typeSpec(el.querySelector('#n-type').value)?.categories || [];
    el.querySelector('#n-cat-f').hidden = !cats.length;
    el.querySelector('#n-cat').innerHTML = '<option value="">请选择</option>' + cats.map((c) => `<option>${esc(c)}</option>`).join('');
  };
  el.querySelectorAll('input[name=subj]').forEach((r) => r.addEventListener('change', fillTypes));
  el.querySelector('#n-type').addEventListener('change', fillCats);
  fillTypes();
  const tags = tagInput(el.querySelector('#n-tags'), [], null);
  el.querySelector('[data-r="0"]').addEventListener('click', d.close);
  const submit = async () => {
    const type = el.querySelector('#n-type').value;
    const cats = typeSpec(type)?.categories || [];
    const category = el.querySelector('#n-cat').value;
    const t = tags.get();
    const err = el.querySelector('#n-err');
    if (cats.length && !category) { err.textContent = '请选择分类'; return; }
    if (!t.length) { err.textContent = '请至少添加一个主题标签'; return; }
    try {
      const nd = await API.post('/essays', { subject: subj(), type, category: cats.length ? category : '', tags: t, title: el.querySelector('#n-title').value.trim() });
      d.close();
      openDrawer(false);
      go(nd.essay.id);
      loadList();
    } catch (e) { err.textContent = e.message; }
  };
  el.querySelector('[data-r="1"]').addEventListener('click', submit);
  el.querySelector('#new-form').addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  el.querySelector('#n-type').focus();
}

async function deleteEssay(id) {
  const e = S.list.find((x) => x.id === id);
  if (!(await confirmDialog('删除作文', `删除「${e ? e.title || '未命名' : id}」及其全部标注、评分和版本？此操作不可撤销。`, '删除'))) return;
  try {
    await API.del(`/essays/${id}`);
    if (S.id === id) closeEssay();
    loadList();
    toast('已删除');
  } catch (err) { errToast(err); }
}

// ---------------------------------------------------------------- 路由

function parseHash() {
  if (/^#\/highlights\/?$/.test(location.hash)) return { highlights: true };
  const m = location.hash.match(/^#\/e\/([^/?#]+)(\/versions)?/);
  return m ? { id: decodeURIComponent(m[1]), view: m[2] ? 'versions' : 'review' } : null;
}

function go(id, view = S.view) {
  const h = `#/e/${encodeURIComponent(id)}${view === 'versions' ? '/versions' : ''}`;
  if (location.hash !== h) location.hash = h;
  else route();
}

function route() {
  const r = parseHash();
  if (r && r.highlights) {
    S.view = 'highlights';
    pop.hide(true);
    loadHighlights();
    renderAll();
    reportUI();
    return;
  }
  if (!r) { if (S.view === 'highlights') S.view = 'review'; if (S.id) closeEssay(); else renderAll(); return; }
  const wasHL = S.view === 'highlights';
  const viewChanged = S.view !== r.view;
  S.view = r.view;
  if (r.id !== S.id) loadEssay(r.id);
  else if (viewChanged) { renderAll(); if (wasHL) reportUI(); }
}

// ---------------------------------------------------------------- SSE

function connectSSE() {
  let es;
  try { es = new EventSource(API.eventsURL()); } catch { return; }
  const onEv = (m) => {
    let ev;
    try { ev = JSON.parse(m.data); } catch { return; }
    handleEvent(ev);
  };
  for (const t of ['essay', 'annotations', 'evaluations', 'versions', 'deleted', 'activity', 'ui-context']) es.addEventListener(t, onEv);
  es.onmessage = onEv;
  es.onopen = () => document.body.classList.remove('sse-down');
  es.onerror = () => document.body.classList.add('sse-down');
}

const fetchUISoon = debounce(() => API.get('/ui-context').then((u) => { S.ui = u; renderSees(); }).catch(() => {}), 300);

function handleEvent(ev) {
  if (ev.type === 'activity') {
    if (ev.activity && !S.activity.some((a) => a.id === ev.activity.id)) {
      S.activity.unshift(ev.activity);
      renderActivity();
    }
    return;
  }
  if (ev.type === 'ui-context') { fetchUISoon(); return; }
  if (S.view === 'highlights' || S.hl.loaded) loadHighlightsSoon();
  if (ev.essayId && ev.essayId === S.id) {
    if (ev.type === 'deleted') { toast('当前作文已被删除'); closeEssay(); }
    else reloadSoon();
  }
  refreshListSoon();
}

// ---------------------------------------------------------------- 启动

function bind() {
  editor = new Editor($('#editor'), {
    onInput,
    onSelection,
    onHover: (id) => setHot(id && markIdSet().has(id) ? null : id, 'text'),
    onRequestAnnotate: (sel) => {
      if (S.pen) { penContent(sel); return; }
      requestAnnotate(sel, () => editor.rangeRect(sel.start, sel.end));
    },
    onKeyDown: (e) => {
      if (pop.handleEditorKey(e)) return;
      if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); saveContent(); }
    },
    onLayout: () => { editor.syncHeight(); editor.renderNums(); layoutMargins(); },
  });

  // 阅读视图（落到段后）
  const reading = $('#reading');
  reading.addEventListener('mouseup', (e) => {
    setTimeout(() => {
      const s = readingSelection(reading);
      if (!s) {
        const m = S.pen && e.target.closest('mark.k-mark');
        if (m) API.del(`/annotations/${m.dataset.id}`).then(reloadCurrent).catch(errToast);
        return;
      }
      S.lastSel = { start: s.start, end: s.end };
      if (S.pen) { penContent(s); window.getSelection().removeAllRanges(); return; }
      const rect = s.rect;
      requestAnnotate({ start: s.start, end: s.end }, () => rect);
    }, 0);
  });
  reading.addEventListener('mouseover', (e) => {
    const m = e.target.closest('[data-id]');
    const id = m ? m.dataset.id : null;
    setHot(id && markIdSet().has(id) ? null : id, 'reading');
  });
  reading.addEventListener('mouseleave', () => setHot(null));

  // 边注：悬停互相高亮、点击固定展开、动作
  for (const m of [$('#mL'), $('#mR')]) {
    m.addEventListener('mouseover', (e) => {
      const n = e.target.closest('.note');
      setHot(n ? n.dataset.id : null, 'margin');
    });
    m.addEventListener('mouseleave', () => setHot(null, 'margin'));
  }
  document.addEventListener('click', (e) => {
    const a = e.target.closest('[data-act]');
    if (a) { e.preventDefault(); act(a.dataset.act, a); return; }
    const g = e.target.closest('[data-goto]');
    if (g) { go(S.id, g.dataset.goto); return; }
    const n = e.target.closest('.note, sup.sn');
    if (n && n.dataset.id) {
      S.pinnedId = S.pinnedId === n.dataset.id ? null : n.dataset.id;
      renderMarks();
      return;
    }
    if (S.pinnedId && !e.target.closest('.popover')) { S.pinnedId = null; if (S.detail) renderMarks(); }
  });

  $('#btn-drawer').addEventListener('click', () => openDrawer(!$('#drawer').classList.contains('open')));
  $('#drawer-back').addEventListener('click', () => openDrawer(false));
  $('#drawer-close').addEventListener('click', () => openDrawer(false));
  $('#empty-open').addEventListener('click', () => openDrawer(true));
  $('#empty-new').addEventListener('click', newEssayDialog);
  $('#btn-new').addEventListener('click', newEssayDialog);
  $('#list-q').addEventListener('input', debounce((e) => { S.listQ = e.target.value.trim(); loadList(); }, 250));
  $('#tag-filter').addEventListener('click', (e) => {
    const b = e.target.closest('[data-tag]');
    if (!b) return;
    S.listTag = S.listTag === b.dataset.tag ? '' : b.dataset.tag;
    renderList();
  });
  $('#tree').addEventListener('click', (e) => {
    const del = e.target.closest('[data-del]');
    if (del) { e.stopPropagation(); deleteEssay(del.dataset.del); return; }
    const col = e.target.closest('[data-col]');
    if (col) { const k = col.dataset.col; if (S.collapsed.has(k)) S.collapsed.delete(k); else S.collapsed.add(k); renderList(); return; }
    const o = e.target.closest('[data-open]');
    if (o) { go(o.dataset.open, 'review'); if (narrowMQ.matches) openDrawer(false); renderList(); }
  });
  $('#tree').addEventListener('keydown', (e) => {
    const o = e.target.closest('[data-open]');
    if (o && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); o.click(); }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && narrowMQ.matches && $('#drawer').classList.contains('open') && !document.querySelector('.dialog-backdrop')) openDrawer(false);
  });

  document.querySelectorAll('[data-view]').forEach((b) => b.addEventListener('click', () => { if (S.id) go(S.id, b.dataset.view); }));
  $('#expand-all').addEventListener('click', () => {
    S.expandAll = !S.expandAll;
    store.set('expandAll', S.expandAll ? '1' : '0');
    S.expanded.clear();
    renderMarks();
  });

  $('#hl-q').addEventListener('input', debounce((e) => { S.hl.q = e.target.value.trim(); renderHighlights(); }, 150));
  $('#hl-group').addEventListener('change', (e) => { S.hl.group = e.target.value; store.set('hlGroup', S.hl.group); renderHighlights(); });
  $('#hl-subj').addEventListener('click', (e) => {
    const b = e.target.closest('[data-subj]');
    if (!b) return;
    S.hl.subj = b.dataset.subj;
    renderHighlights();
  });
  $('#hl-list').addEventListener('click', (e) => {
    const c = e.target.closest('[data-hl-col]');
    if (c) { const k = c.dataset.hlCol; if (S.hl.collapsed.has(k)) S.hl.collapsed.delete(k); else S.hl.collapsed.add(k); renderHighlights(); return; }
    if (e.target.closest('.md a')) return;
    const it = e.target.closest('[data-hl-open]');
    // 拦住冒泡：document 上的点击处理会把刚固定的边注取消。
    if (it && !e.metaKey && !e.ctrlKey && !e.shiftKey) { e.preventDefault(); e.stopPropagation(); openAnnotation(it.dataset.hlOpen, it.dataset.aid); }
  });
  $('#edit-toggle').addEventListener('click', () => {
    S.editing = !S.editing;
    if (!S.editing && isDirty()) saveContent();
    renderMarks();
    if (S.editing) editor.ta.focus();
  });
  narrowMQ.addEventListener('change', () => {
    S.editing = false;
    // 跨过断点：宽屏恢复记住的抽屉状态，窄屏收起覆盖层。
    openDrawer(!narrowMQ.matches && store.get('drawer', 'closed') === 'open', { focus: false });
    if (S.detail) renderMarks();
  });
  $('#pen-toggle').addEventListener('click', () => setPen(!S.pen));
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && S.pen && !document.querySelector('.dialog-backdrop')) setPen(false); });
  $('#info').addEventListener('mouseup', (e) => { if (S.pen && S.detail && e.target.closest('.pinfo-text')) penPrompt(e); });
  editor.ta.addEventListener('mouseup', () => {
    const s = editor.selection();
    if (S.pen && s.start === s.end) penContent(s);
  });
  $('#info').addEventListener('click', (e) => {
    if (e.target.closest('.pinfo-more')) { S.promptOpen = !S.promptOpen; renderInfo(); return; }
    if (S.pen) return;
    if (window.getSelection()?.toString()) return; // 允许选中复制题目
    editInfoDialog();
  });
  $('#info').addEventListener('keydown', (e) => { if ((e.key === 'Enter' || e.key === ' ') && e.target.id === 'info') { e.preventDefault(); editInfoDialog(); } });
  $('#scorebar').addEventListener('click', (e) => {
    if (e.target.closest('#sb-toggle')) { S.scoreOpen = !S.scoreOpen; renderScorebar(); }
  });
  const title = $('#title');
  const saveTitle = async () => {
    if (!S.detail || title.value.trim() === S.detail.essay.title) return;
    try { applyDetail(await API.patch(`/essays/${S.id}`, { title: title.value.trim() })); refreshListSoon(); } catch (e) { errToast(e); }
  };
  title.addEventListener('blur', saveTitle);
  title.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); title.blur(); } });
  title.addEventListener('input', fitTitle);
  // 首次渲染时栏宽可能还没定下来：宽度一变就重新量高度（只看宽度，避免改高度时自触发）。
  let titleW = 0;
  new ResizeObserver(() => { if (title.clientWidth && title.clientWidth !== titleW) { titleW = title.clientWidth; fitTitle(); } }).observe(title);

  $('#versions').addEventListener('change', (e) => {
    const s = e.target.closest('select[data-pick]');
    if (s) { setPick(S.detail, { [s.dataset.pick]: s.value }); renderVersionsView(); }
  });
  $('#versions').addEventListener('click', (e) => {
    const b = e.target.closest('[data-pickv]');
    if (b) { setPick(S.detail, { from: b.dataset.pickv }); renderVersionsView(); }
  });

  document.querySelectorAll('[data-prompt]').forEach((b) => b.addEventListener('click', () => copyPrompt(b.dataset.prompt)));
  $('#dock-toggle').addEventListener('click', () => {
    const c = document.body.classList.toggle('dock-min');
    store.set('dock', c ? 'min' : 'open');
    $('#dock-toggle').setAttribute('aria-expanded', !c);
  });
  const dockPref = store.get('dock', '');
  if (dockPref === 'min' || (!dockPref && narrowMQ.matches)) document.body.classList.add('dock-min');

  window.addEventListener('hashchange', route);
  window.addEventListener('resize', debounce(() => { if (S.detail && S.view === 'review') renderMarks(); }, 100));
  // 抽屉推开/收回改变主区宽度：动画结束后重排边注。
  $('#drawer').addEventListener('transitionend', (e) => { if (e.propertyName === 'width' && S.detail && S.view === 'review') renderMarks(); });
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden' && isDirty()) saveContent(); });
  window.addEventListener('beforeunload', (e) => { if (isDirty()) { saveContent(); e.preventDefault(); e.returnValue = ''; } });
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(() => { if (S.detail) renderMarks(); });
}

async function init() {
  bind();
  try {
    S.meta = await API.get('/meta');
  } catch (e) {
    $('#empty').hidden = false;
    $('#empty').querySelector('p').textContent = '无法连接服务端：' + e.message + (e.status === 401 ? '（需要访问令牌：用 #token=… 打开页面）' : '');
    return;
  }
  const mcp = $('#mcp-status');
  mcp.innerHTML = S.meta.mcp && S.meta.mcp.enabled ? `<span class="dot ring"></span>MCP · ${esc(S.meta.mcp.path || '/mcp')}` : '<span class="dot off"></span>MCP 未开启';
  mcp.title = S.meta.mcp && S.meta.mcp.enabled ? '本地 Agent 可通过 MCP（StreamableHTTP）接入' : '启动时加 -mcp 开启';
  API.get('/activity?limit=50').then((a) => { S.activity = a || []; renderActivity(); }).catch(() => renderActivity());
  API.get('/ui-context').then((u) => { S.ui = u; renderSees(); }).catch(() => {});
  connectSSE();
  if (!narrowMQ.matches && store.get('drawer', 'closed') === 'open') openDrawer(true, { focus: false });
  else loadList();
  if (parseHash()) route();
  else { renderAll(); }

  window.essayPilot = {
    get currentId() { return S.id; },
    get detail() { return S.detail; },
    get meta() { return S.meta; },
    open: async (id) => { go(id, 'review'); await new Promise((r) => setTimeout(r, 0)); if (S.id !== id) await loadEssay(id); return S.detail; },
    reload: () => reloadCurrent(),
    flush: () => saveContent(),
    selection: () => { const s = editor.selection(); return { ...s, text: editor.value.slice(s.start, s.end) }; },
  };
  import('./webmcp.js').then((m) => m.init(window.essayPilot)).catch((e) => console.warn('WebMCP 初始化失败', e));
}

init();
