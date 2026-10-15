// EssayPilot v2 前端主模块：状态、列表抽屉、批阅视图、版本视图、SSE、底部 Agent 栏。

import * as API from './api.js';
import { esc, icon, debounce, fmtTime, fmtScore, SUBJECT_LABEL, shortDim, authorName, isAgent, toast, errToast, openDialog, confirmDialog, tagInput, compressImage, copyText } from './util.js';
import { Editor, renderReading, readingSelection, paragraphsOf, paragraphIndexAt } from './editor.js';
import { layoutSide, belowNotesHTML } from './margin.js';
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
  density: store.get('density', 'cluster'),
  editing: false,
  hotId: null,
  hotFrom: null,
  pinnedId: null,
  caretId: null,
  expanded: new Set(),
  pending: null,
  lastSel: null,
  infoOpen: false,
  scoreOpen: false,
  activity: [],
  ui: null,
  saveState: '',
  pendingType: null,
};

const narrowMQ = window.matchMedia('(max-width: 899px)');
const isBelow = () => narrowMQ.matches || S.density === 'below';

// ---------------------------------------------------------------- 派生数据

function derive() {
  const text = editor.value;
  const vis = S.anns.filter((a) => a.status === 'open' && !a.parentId && a.start < a.end && a.end <= text.length);
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
  return { text, vis, numbers, replies, orphans };
}

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
    S.expanded.clear();
    S.pendingType = null;
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
    $('#empty').hidden = true;
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
  if (location.hash) history.replaceState(null, '', location.pathname + location.search);
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
      if (a.status !== 'open' || a.parentId || a.start >= a.end) continue;
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
  if (!S.id) { putUISoon({ essayId: '', selectionStart: 0, selectionEnd: 0, selectionText: '' }); return; }
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
  document.body.classList.toggle('no-essay', !d);
  $('#empty').hidden = !!d;
  renderTop();
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
  if (document.activeElement !== t) t.value = d.essay.title;
  renderWordCount();
  renderMarks();
  if (S.view === 'versions') renderVersionsView();
  renderSees();
}

function renderTop() {
  const d = S.detail;
  const crumbs = $('#crumbs');
  if (!d) {
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
  document.title = d ? `${d.essay.title || '未命名'} · EssayPilot` : 'EssayPilot';
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

function renderInfo() {
  const d = S.detail;
  const e = d.essay;
  const spec = typeSpec(e.type);
  $('#info-toggle').innerHTML = `${S.infoOpen ? '▾' : '▸'} 题目信息 · ${esc(SUBJECT_LABEL[e.subject])} · ${esc(spec ? spec.shortLabel || spec.label : e.type)}${e.category ? ' · ' + esc(e.category) : ''} ${e.tags.map((t) => `<span class="tagref">#${esc(t)}</span>`).join(' ')}`;
  $('#info-toggle').setAttribute('aria-expanded', S.infoOpen);
  const box = $('#info');
  box.hidden = !S.infoOpen;
  if (!S.infoOpen) { box.dataset.for = ''; return; }
  if (box.dataset.for === e.id + e.updatedAt && box.contains(document.activeElement)) return;
  box.dataset.for = e.id + e.updatedAt;
  const types = S.meta.types.filter((t) => t.subject === e.subject);
  const curType = S.pendingType || e.type;
  const cats = typeSpec(curType)?.categories || [];
  box.innerHTML = `<div class="info-grid">
    <div class="field"><label for="f-type">题型</label><select id="f-type" class="input">${types.map((t) => `<option value="${t.id}"${t.id === curType ? ' selected' : ''}>${esc(t.label)}</option>`).join('')}</select></div>
    ${cats.length ? `<div class="field"><label for="f-cat">分类${S.pendingType ? '（请选择以完成题型切换）' : ''}</label><select id="f-cat" class="input${S.pendingType ? ' need' : ''}">${S.pendingType ? '<option value="">请选择</option>' : ''}${cats.map((c) => `<option${c === e.category && !S.pendingType ? ' selected' : ''}>${esc(c)}</option>`).join('')}</select></div>` : ''}
    <div class="field wide"><label>主题标签</label><div id="f-tags"></div></div>
    <div class="field wide"><label for="f-prompt">题目要求</label><textarea id="f-prompt" class="input" rows="4" placeholder="粘贴题目要求；也可以直接粘贴题目图片">${esc(e.prompt)}</textarea></div>
    <div class="field wide"><label>题目图片</label><div class="img-row">${e.promptImage ? `<img class="plate prompt-img" src="${esc(e.promptImage)}" alt="题目图片">` : '<span class="muted small">无</span>'}
      <label class="btn btn-secondary btn-sm">${icon('image', 14)} 选择图片<input type="file" accept="image/*" id="f-img" hidden></label>
      ${e.promptImage ? '<button class="btn btn-ghost btn-sm" id="f-img-del">删除图片</button>' : ''}<span class="muted xsmall">也可在题目要求框内粘贴</span></div></div>
  </div>`;
  const patchEssay = async (body) => {
    try {
      const nd = await API.patch(`/essays/${S.id}`, body);
      applyDetail(nd);
      refreshListSoon();
    } catch (err) { errToast(err); box.dataset.for = ''; renderInfo(); }
  };
  $('#f-type').addEventListener('change', (ev) => {
    const t = ev.target.value;
    const c = typeSpec(t)?.categories || [];
    if (!c.length) { S.pendingType = null; patchEssay({ type: t, category: '' }); }
    else if (c.includes(e.category)) { S.pendingType = null; patchEssay({ type: t }); }
    else { S.pendingType = t === e.type ? null : t; box.dataset.for = ''; renderInfo(); }
  });
  $('#f-cat')?.addEventListener('change', (ev) => {
    if (!ev.target.value) return;
    const body = { category: ev.target.value };
    if (S.pendingType) body.type = S.pendingType;
    S.pendingType = null;
    patchEssay(body);
  });
  tagInput($('#f-tags'), e.tags, (tags) => {
    if (!tags.length) { toast('至少保留一个主题标签', 'err'); return; }
    patchEssay({ tags });
  });
  const pr = $('#f-prompt');
  pr.addEventListener('change', () => patchEssay({ prompt: pr.value }));
  pr.addEventListener('paste', async (ev) => {
    const f = [...(ev.clipboardData?.files || [])].find((x) => x.type.startsWith('image/'));
    if (!f) return;
    ev.preventDefault();
    try { patchEssay({ promptImage: await compressImage(f) }); } catch (err) { errToast(err); }
  });
  $('#f-img').addEventListener('change', async (ev) => {
    const f = ev.target.files[0];
    if (!f) return;
    try { patchEssay({ promptImage: await compressImage(f) }); } catch (err) { errToast(err); }
  });
  $('#f-img-del')?.addEventListener('click', () => patchEssay({ promptImage: '' }));
}

let prevTops = new Map();

function renderMarks() {
  if (!S.detail) return;
  const D = derive();
  const below = isBelow();
  const desk = $('#desk');
  desk.classList.toggle('below', below);
  $('#density').querySelectorAll('button').forEach((b) => { b.classList.toggle('on', b.dataset.d === (below ? 'below' : 'cluster')); b.disabled = narrowMQ.matches; });
  const showEditor = !below || S.editing;
  $('#editor').hidden = !showEditor;
  $('#reading').hidden = showEditor;
  const et = $('#edit-toggle');
  et.hidden = !below;
  et.innerHTML = S.editing ? `${icon('check', 14)} 完成编辑` : `${icon('pen', 14)} 编辑正文`;

  editor.render({ anns: D.vis, numbers: showEditor && !below ? D.numbers : new Map(), pending: S.pending, hotId: S.hotId });
  S._D = D;
  if (!below) layoutMargins();
  else {
    $('#mL').innerHTML = '';
    $('#mR').innerHTML = '';
    if (!showEditor) {
      const groups = groupByPara(D, paragraphsOf(D.text), null);
      renderReading($('#reading'), D.text, D.vis, D.numbers, (p) => belowNotesHTML(groups.get(p.i), ctxFor(D)), S.pending);
    }
  }
  renderOrphans(D);
}

function groupByPara(D, paras, side) {
  const groups = new Map();
  for (const a of D.vis) {
    if (side === 'L' && isAgent(a.author)) continue;
    if (side === 'R' && !isAgent(a.author)) continue;
    const i = paragraphIndexAt(paras, a.start);
    if (!groups.has(i)) groups.set(i, []);
    groups.get(i).push(a);
  }
  return groups;
}

function ctxFor(D) {
  return { numbers: D.numbers, replies: D.replies, openId: openId(), hotId: S.hotId, expanded: S.expanded, markTop: (id) => editor.markTop(id) };
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
  const nu = D.vis.filter((a) => !isAgent(a.author)).length;
  $('#head-l').textContent = `我的批注 ${nu || ''}`;
  $('#head-r').textContent = `Agent ${n - nu || ''}`;
}

function renderOrphans(D) {
  const el = $('#orphans');
  if (!D.orphans.length) { el.innerHTML = ''; return; }
  el.innerHTML = `<div class="orph-h">孤立标注 ${D.orphans.length}</div>` + D.orphans.map((a) => `<div class="orph">
    <span class="${isAgent(a.author) ? 'ag' : 'us'}">“${esc(a.quote.length > 50 ? a.quote.slice(0, 50) + '…' : a.quote)}” 在正文中找不到${a.note ? ' · ' + esc(a.note) : ''} <span class="muted xsmall">${esc(authorName(a.author))}</span></span>
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

function openDrawer(open) {
  const dr = $('#drawer');
  dr.classList.toggle('open', open);
  $('#drawer-back').hidden = !open;
  dr.setAttribute('aria-hidden', !open);
  if (open) { loadList(); setTimeout(() => $('#list-q').focus(), 50); }
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
  if (!r) { if (S.id) closeEssay(); else renderAll(); return; }
  const viewChanged = S.view !== r.view;
  S.view = r.view;
  if (r.id !== S.id) loadEssay(r.id);
  else if (viewChanged) renderAll();
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
    onHover: (id) => setHot(id, 'text'),
    onRequestAnnotate: (sel) => {
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
  reading.addEventListener('mouseup', () => {
    setTimeout(() => {
      const s = readingSelection(reading);
      if (!s) return;
      S.lastSel = { start: s.start, end: s.end };
      const rect = s.rect;
      requestAnnotate({ start: s.start, end: s.end }, () => rect);
    }, 0);
  });
  reading.addEventListener('mouseover', (e) => {
    const m = e.target.closest('[data-id]');
    setHot(m ? m.dataset.id : null, 'reading');
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
    if (o) { go(o.dataset.open, 'review'); if (window.innerWidth < 1100) openDrawer(false); renderList(); }
  });
  $('#tree').addEventListener('keydown', (e) => {
    const o = e.target.closest('[data-open]');
    if (o && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); o.click(); }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && $('#drawer').classList.contains('open') && !document.querySelector('.dialog-backdrop')) openDrawer(false);
  });

  document.querySelectorAll('[data-view]').forEach((b) => b.addEventListener('click', () => { if (S.id) go(S.id, b.dataset.view); }));
  $('#density').addEventListener('click', (e) => {
    const b = e.target.closest('[data-d]');
    if (!b || narrowMQ.matches) return;
    S.density = b.dataset.d;
    store.set('density', S.density);
    S.editing = false;
    renderMarks();
  });
  $('#edit-toggle').addEventListener('click', () => {
    S.editing = !S.editing;
    if (!S.editing && isDirty()) saveContent();
    renderMarks();
    if (S.editing) editor.ta.focus();
  });
  narrowMQ.addEventListener('change', () => { S.editing = false; if (S.detail) renderMarks(); });
  $('#info-toggle').addEventListener('click', () => { S.infoOpen = !S.infoOpen; renderInfo(); });
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
  loadList();
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
