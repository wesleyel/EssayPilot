// 版本视图：左侧版本时间线 + 分数走势，中间词级 diff，右侧评分卡。

import { esc, fmtTime, fmtScore, authorName, shortDim } from './util.js';
import { diffText, countChanges } from './diff.js';

const picks = new Map(); // essayId → {from, to}

function versionNums(versions) {
  const m = new Map();
  versions.forEach((v, i) => m.set(v.id, versions.length - i));
  return m;
}

function textOf(pick, d, content) {
  if (pick === 'current') return content;
  if (pick === 'polished') return d.evaluations[0]?.polished || '';
  if (pick.startsWith('v:')) return d.versions.find((v) => 'v:' + v.id === pick)?.content ?? '';
  return '';
}

function labelOf(pick, d, nums) {
  if (pick === 'current') return '当前草稿';
  if (pick === 'polished') return '最新评分润色稿';
  const v = d.versions.find((x) => 'v:' + x.id === pick);
  return v ? `v${nums.get(v.id)}` : '?';
}

function defaults(d) {
  if (d.evaluations[0]?.polished) return { from: 'current', to: 'polished' };
  if (d.versions.length) return { from: 'v:' + d.versions[0].id, to: 'current' };
  return { from: 'current', to: 'current' };
}

export function getPick(d) {
  let p = picks.get(d.essay.id);
  const valid = (k) => k === 'current' || (k === 'polished' && d.evaluations[0]?.polished) || (k.startsWith('v:') && d.versions.some((v) => 'v:' + v.id === k));
  if (!p || !valid(p.from) || !valid(p.to)) { p = defaults(d); picks.set(d.essay.id, p); }
  return p;
}

export function setPick(d, patch) {
  const p = { ...getPick(d), ...patch };
  if (patch.from && p.to === p.from) p.to = p.from === 'current' ? (d.evaluations[0]?.polished ? 'polished' : 'current') : 'current';
  picks.set(d.essay.id, p);
}

function trendSVG(evals, w = 168, h = 44) {
  const pts = [...evals].reverse();
  if (!pts.length) return '<div class="muted small">尚无评分</div>';
  const max = pts[0].maxScore || 20;
  const xs = (i) => (pts.length === 1 ? w / 2 : 8 + (i * (w - 16)) / (pts.length - 1));
  const ys = (s) => h - 6 - (s / max) * (h - 12);
  const poly = pts.map((e, i) => `${xs(i).toFixed(1)},${ys(e.score).toFixed(1)}`).join(' ');
  return `<svg class="trend" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-label="分数走势">
    <line x1="0" x2="${w}" y1="${h - 6}" y2="${h - 6}" stroke="var(--color-divider)"/>
    <polyline points="${poly}" fill="none" stroke="var(--color-accent)" stroke-width="1.5"/>
    ${pts.map((e, i) => `<circle cx="${xs(i).toFixed(1)}" cy="${ys(e.score).toFixed(1)}" r="2.5" fill="var(--color-accent)"><title>${fmtScore(e.score)}/${fmtScore(e.maxScore)} · ${fmtTime(e.createdAt)}</title></circle>`).join('')}
  </svg>`;
}

function diffHTML(ops) {
  return ops.map((o) => {
    const t = esc(o.text);
    if (o.op === 'del') return `<del>${t}</del>`;
    if (o.op === 'ins') return `<ins>${t}</ins>`;
    return t;
  }).join('');
}

export function scoreCardHTML(ev, prev, meta, subject, pendingCorr) {
  if (!ev) {
    return `<div class="sc-empty"><div class="sc-big muted">—</div><p class="muted small">尚未评分。用底部「复制 grade_essay」把评分提示交给本地 Agent。</p></div>${corrListHTML(pendingCorr)}`;
  }
  const dims = meta.dimensions?.[subject] || {};
  const keys = meta.dimensionKeys || Object.keys(ev.dimensions || {});
  const rows = keys.map((k) => {
    const d = ev.dimensions?.[k];
    if (!d) return '';
    const p = prev?.dimensions?.[k];
    const delta = p ? Math.round((d.score - p.score) * 10) / 10 : null;
    const dcls = delta > 0 ? 'up' : delta < 0 ? 'down' : 'zero';
    return `<tr title="${esc(d.feedback || '')}"><td>${esc(shortDim(dims[k]?.label || k))}</td><td class="r">${fmtScore(d.score)} / ${fmtScore(d.maxScore)}</td><td class="r ${dcls}">${delta === null ? '—' : (delta > 0 ? '+' : '') + delta}</td></tr>`;
  }).join('');
  return `<div class="sc-head"><span class="sc-big">${fmtScore(ev.score)}</span><span class="sc-max">/ ${fmtScore(ev.maxScore)}</span>${ev.band ? `<span class="tag tag-accent">${esc(ev.band)}</span>` : ''}</div>
    <div class="muted small">${esc(authorName(ev.author))} · ${fmtTime(ev.createdAt)}</div>
    <table class="table sc-table"><thead><tr><th>维度</th><th class="r">得分</th><th class="r">较上一次</th></tr></thead><tbody>${rows}</tbody></table>
    ${ev.overallComment ? `<div class="sc-sec"><h4>总评</h4><p>${esc(ev.overallComment)}</p></div>` : ''}
    ${ev.strengths?.length ? `<div class="sc-sec"><h4>亮点</h4><ul>${ev.strengths.map((s) => `<li>${esc(s)}</li>`).join('')}</ul></div>` : ''}
    ${ev.weaknesses?.length ? `<div class="sc-sec"><h4>短板</h4><ul>${ev.weaknesses.map((s) => `<li>${esc(s)}</li>`).join('')}</ul></div>` : ''}
    ${corrListHTML(pendingCorr)}`;
}

function corrListHTML(list) {
  if (!list.length) return '';
  return `<div class="sc-sec"><h4>纠错 · ${list.length} 条待处理 ${list.length > 1 ? `<button class="lnk" data-act="apply-all" data-ids="${list.map((a) => esc(a.id)).join(',')}">全部采纳</button>` : ''}</h4>
    ${list.map((a) => `<div class="corr-row"><span><s>${esc(a.quote)}</s> → ${esc(a.suggestion) || '<i>删去</i>'} <span class="muted xsmall">${esc(a.label || '')}</span></span><button class="btn btn-ghost btn-xs" data-act="apply" data-id="${esc(a.id)}">采纳</button></div>`).join('')}</div>`;
}

export function renderVersions(root, { detail: d, meta, content, dirty }) {
  const nums = versionNums(d.versions);
  const p = getPick(d);
  const subject = d.essay.subject;
  const options = [['current', '当前草稿'], ...(d.evaluations[0]?.polished ? [['polished', '最新评分润色稿']] : []), ...d.versions.map((v) => ['v:' + v.id, `v${nums.get(v.id)} · ${v.note || ''}${v.score != null ? ' · ' + fmtScore(v.score) : ''}`])];
  const sel = (name, val) => `<select class="input sel" data-pick="${name}" aria-label="${name === 'from' ? '对比起点' : '对比终点'}">${options.map(([k, l]) => `<option value="${esc(k)}"${k === val ? ' selected' : ''}>${esc(l)}</option>`).join('')}</select>`;

  const a = textOf(p.from, d, content), b = textOf(p.to, d, content);
  const ops = diffText(a, b, subject);
  const changes = countChanges(ops);

  const latestVer = d.versions[0];
  const curNote = dirty ? '未保存的改动' : latestVer && latestVer.content === content ? `与 v${nums.get(latestVer.id)} 相同` : '有未存为版本的改动';
  const tl = [`<button class="tl-item cur${p.from === 'current' || p.to === 'current' ? ' sel' : ''}" data-pickv="current"><span class="dot hollow"></span><span class="tl-t">当前草稿</span><span class="tl-s">${esc(curNote)}</span></button>`];
  for (const v of d.versions) {
    const n = nums.get(v.id);
    const scored = v.evaluationId != null;
    const isSel = p.from === 'v:' + v.id || p.to === 'v:' + v.id;
    tl.push(`<button class="tl-item${scored ? ' scored' : ''}${isSel ? ' sel' : ''}" data-pickv="v:${esc(v.id)}"><span class="dot"></span>
      <span class="tl-t">v${n} · ${scored ? `<b>${fmtScore(v.score)}</b>/${fmtScore(d.evaluations.find((e) => e.id === v.evaluationId)?.maxScore ?? '')}` : '手动保存'}</span>
      <span class="tl-s">${scored ? `评分快照 · ${esc(authorName(v.author))}` : v.note ? `“${esc(v.note)}”` : esc(authorName(v.author))}</span>
      <span class="tl-s">${fmtTime(v.createdAt)} · ${v.wordCount} ${subject === 'english' ? '词' : '字'}</span></button>`);
  }

  const fromV = p.from.startsWith('v:') ? d.versions.find((v) => 'v:' + v.id === p.from) : null;
  let ev = d.evaluations[0];
  if (fromV && fromV.evaluationId) ev = d.evaluations.find((e) => e.id === fromV.evaluationId) || ev;
  const evIdx = ev ? d.evaluations.indexOf(ev) : -1;
  const prev = evIdx >= 0 ? d.evaluations[evIdx + 1] : null;
  const pending = d.annotations.filter((x) => x.kind === 'correction' && x.status === 'open' && x.suggestion != null && !x.parentId);

  root.innerHTML = `<div class="vgrid">
    <aside class="v-left"><h6 class="kicker">版本</h6><div class="tl">${tl.join('')}</div>
      <div class="sp"></div><div class="muted small">分数走势</div>${trendSVG(d.evaluations)}</aside>
    <section class="v-mid">
      <div class="v-bar"><span class="muted small">对比</span>${sel('from', p.from)}<span class="muted">→</span>${sel('to', p.to)}<span class="sp"></span><span class="muted small">${subject === 'english' ? '词级' : '字级'} diff · 改动 ${changes} 处</span></div>
      <h2 class="v-title">${esc(d.essay.title)}</h2>
      <div class="diff essay-text">${a === b && !a ? '<span class="muted">（空）</span>' : diffHTML(ops)}</div>
      <div class="v-actions">
        ${p.to === 'polished' ? '<button class="btn btn-primary" data-act="adopt-polished">全部采纳为新版本</button>' : ''}
        <button class="btn btn-secondary" data-act="save-version">保存当前版本</button>
        ${fromV ? `<button class="btn btn-secondary" data-act="restore" data-id="${esc(fromV.id)}">恢复此版本（v${nums.get(fromV.id)}）</button><button class="btn btn-ghost" data-act="delete-version" data-id="${esc(fromV.id)}">删除 v${nums.get(fromV.id)}</button>` : ''}
      </div>
      <p class="muted small">${esc(labelOf(p.from, d, nums))} → ${esc(labelOf(p.to, d, nums))}：删去的显示为删除线，新增的加下划线。</p>
    </section>
    <aside class="v-right">${scoreCardHTML(ev, prev, meta, subject, pending)}</aside>
  </div>`;
}
