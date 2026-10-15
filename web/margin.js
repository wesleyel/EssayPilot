// Tufte 边注：左页边 = 重点与亮点，右页边 = 问题、疑问与纠错；作者用颜色区分。
// 每段一个簇，簇高度不超过段落，放不下就折叠尾部；“全部展开”时不折叠、边注全文显示，簇依次向下推。

import { esc, authorName, isAgent, md, mdLine, mdInline } from './util.js';

export const KIND_LABEL = { highlight: '重点', good: '亮点', issue: '问题', question: '疑问', correction: '纠错' };

// 'L'：重点 / 亮点；'R'：问题 / 疑问 / 纠错。
export const sideOf = (a) => (a.kind === 'highlight' || a.kind === 'good' ? 'L' : 'R');

const clip = (s, n = 60) => (s.length > n ? s.slice(0, n) + '…' : s);

// wide：边注全文（Markdown 块）；否则单行摘要。
function lineHTML(a, wide) {
  if (a.kind === 'correction' && a.suggestion != null) {
    return `<s>${esc(a.quote)}</s> → ${esc(a.suggestion) || '<i>删去</i>'}`;
  }
  if (a.note) return wide ? md(a.note) : mdLine(a.note);
  if (a.label) return esc(a.label);
  return `<span class="q">“${esc(clip(a.quote, 40))}”</span>`;
}

function replyHTML(r) {
  const who = isAgent(r.author) ? 'Agent' : '我';
  return `<div class="reply ${isAgent(r.author) ? 'ag' : 'us'}" data-rid="${esc(r.id)}">↳ ${who}：${mdInline(r.note).replace(/\n/g, '<br>')}${r.author === 'user' ? ` <button class="lnk" data-act="delete" data-id="${esc(r.id)}">删除</button>` : ''}</div>`;
}

// side: 'L' | 'R' | 'B'（窄屏段后）；full：全部展开模式。
export function noteHTML(a, { num, replies = [], open, hot, side, full: fullMode = false }) {
  const corr = a.kind === 'correction' && a.suggestion != null;
  const mine = a.author === 'user';
  const wide = open || fullMode;
  const cls = `note s-${side} k-${a.kind} ${isAgent(a.author) ? 'ag' : 'us'}${open ? ' open' : ''}${wide ? ' wide' : ''}${hot ? ' hot' : ''}`;
  const numHTML = `<span class="num">${num ?? ''}</span>`;
  const blockNote = wide && !corr && a.note;
  let line = blockNote ? `<div class="nt md">${lineHTML(a, true)}</div>` : `<span class="nt">${lineHTML(a, false)}</span>`;
  if (!open && corr) line += `<button class="lnk" data-act="apply" data-id="${esc(a.id)}">采纳</button>`;
  if (!open && replies.length) line += `<span class="rc" title="${replies.length} 条回复">↳${replies.length}</span>`;
  let full = '';
  if (!open && fullMode && corr && a.note) full = `<div class="nf"><div class="ntx md">${md(a.note)}</div></div>`;
  if (open) {
    const kindTag = `${KIND_LABEL[a.kind] || a.kind}${a.label && a.label !== a.note ? ' · ' + esc(a.label) : ''}`;
    full = `<div class="nf">
      ${corr ? `<div class="corr"><span><s>${esc(a.quote)}</s> → <b>${esc(a.suggestion) || '<i>删去</i>'}</b></span><button class="btn btn-primary btn-xs" data-act="apply" data-id="${esc(a.id)}">采纳</button></div>` : ''}
      ${corr && a.note ? `<div class="ntx md">${md(a.note)}</div>` : ''}
      ${!corr && a.note ? `<div class="ntx quote-ref">“${esc(clip(a.quote, 80))}”</div>` : ''}
      ${replies.map(replyHTML).join('')}
      <div class="nact"><span class="meta">${kindTag} · ${esc(authorName(a.author))}</span>
        <button class="lnk" data-act="reply" data-id="${esc(a.id)}">回复</button>
        ${mine ? `<button class="lnk" data-act="edit" data-id="${esc(a.id)}">编辑</button>` : ''}
        ${corr ? `<button class="lnk" data-act="resolve" data-id="${esc(a.id)}">忽略</button>` : (mine ? '' : `<button class="lnk" data-act="resolve" data-id="${esc(a.id)}">已阅</button>`)}
        <button class="lnk" data-act="delete" data-id="${esc(a.id)}">删除</button></div>
    </div>`;
  }
  return `<div class="${cls}" data-id="${esc(a.id)}"><div class="nl">${numHTML}${line}</div>${full}</div>`;
}

// 在 container 里按段落几何排簇。
// ctx: { numbers:Map, replies:Map(parentId→[]), openId, hotId, expanded:Set, expandAll:bool, markTop(id) }
export function layoutSide(container, side, groups, geo, ctx) {
  container.innerHTML = '';
  let prevBottom = -Infinity;
  for (const g of geo) {
    const items = groups.get(g.i);
    if (!items || !items.length) continue;
    const key = side + ':' + g.i;
    const all = !!ctx.expandAll;
    const expanded = !all && ctx.expanded.has(key);
    const corr = items.filter((a) => a.kind === 'correction' && a.suggestion != null);
    const cl = document.createElement('div');
    cl.className = 'cluster' + (expanded ? ' expanded' : '');
    cl.dataset.key = key;
    let html = '';
    if (corr.length >= 2) {
      html += `<div class="cl-head"><span>本段 ${items.length} 条 · 纠错 ${corr.length}</span><button class="lnk" data-act="apply-all" data-ids="${corr.map((a) => esc(a.id)).join(',')}">全部采纳</button></div>`;
    }
    html += items.map((a) => noteHTML(a, { num: ctx.numbers.get(a.id), replies: ctx.replies.get(a.id) || [], open: a.id === ctx.openId, hot: a.id === ctx.hotId, side, full: all })).join('');
    html += '<button class="cl-more lnk" data-act="expand" data-key="' + key + '" hidden></button>';
    cl.innerHTML = html;
    cl.style.top = g.top + 'px';
    container.appendChild(cl);

    const maxH = Math.max(g.bottom - g.top, 22);
    const notes = [...cl.querySelectorAll(':scope > .note')];
    const more = cl.querySelector('.cl-more');
    let visible = notes;
    if (expanded) {
      more.hidden = false;
      more.textContent = '收起 ▴';
    } else if (!all && cl.offsetHeight > maxH && notes.length > 1) {
      const headH = cl.querySelector('.cl-head')?.offsetHeight || 0;
      more.hidden = false;
      more.textContent = '另 99 条（…）· 展开 ▾';
      const moreH = more.offsetHeight;
      const hs = notes.map((n) => n.offsetHeight + 3);
      const budget = maxH - headH - moreH;
      const vis = new Set();
      let used = 0;
      const ai = notes.findIndex((n) => n.classList.contains('open') || n.classList.contains('hot'));
      if (ai >= 0) { vis.add(ai); used += hs[ai]; }
      for (let i = 0; i < notes.length; i++) {
        if (vis.has(i)) continue;
        if (used + hs[i] > budget) break;
        vis.add(i);
        used += hs[i];
      }
      if (!vis.size) { vis.add(0); }
      const hidden = notes.filter((_, i) => !vis.has(i));
      hidden.forEach((n) => { n.hidden = true; });
      visible = notes.filter((_, i) => vis.has(i));
      if (hidden.length) {
        more.textContent = `另 ${hidden.length} 条（${hidden.map((n) => ctx.numbers.get(n.dataset.id) ?? '').join(' ')}）· 展开 ▾`;
      } else more.hidden = true;
    }
    const h = cl.offsetHeight;
    let desired = g.top;
    const firstId = visible[0] && visible[0].dataset.id;
    const mt = firstId ? ctx.markTop(firstId) : null;
    if (mt !== null && mt !== undefined) desired = mt;
    let top = Math.min(desired, g.bottom - h);
    top = Math.max(top, g.top, prevBottom + 6);
    cl.style.top = top + 'px';
    prevBottom = top + (expanded ? Math.min(h, maxH) : h);
  }
  // 全部展开时边注可能比正文长：撑高页边列，让页面能滚到最后一条。
  container.style.minHeight = ctx.expandAll && prevBottom > 0 ? prevBottom + 'px' : '';
}

// 窄屏没有页边：边注落到段后，两列小字。
export function belowNotesHTML(items, ctx) {
  if (!items || !items.length) return '';
  return `<div class="below-notes">${items.map((a) => noteHTML(a, { num: ctx.numbers.get(a.id), replies: ctx.replies.get(a.id) || [], open: a.id === ctx.openId, hot: a.id === ctx.hotId, side: 'B', full: ctx.expandAll })).join('')}</div>`;
}
