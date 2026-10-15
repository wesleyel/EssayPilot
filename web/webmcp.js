// WebMCP（渐进增强）：浏览器原生支持 document.modelContext / navigator.modelContext 时，
// 把页面能力注册成工具，供浏览器里的 Agent 调用。全部经 REST，带 X-Actor: agent:webmcp。
// 规范仍在变：registerTool 可能同步或返回 Promise，可能不支持 signal，重名会抛 InvalidStateError。

import * as API from './api.js';

const OPT = { actor: 'agent:webmcp' };
const KINDS = ['highlight', 'good', 'issue', 'question', 'correction'];

const ok = (data) => ({ content: [{ type: 'text', text: typeof data === 'string' ? data : JSON.stringify(data, null, 2) }] });
const fail = (e) => ({ content: [{ type: 'text', text: '错误：' + (e && e.message ? e.message : String(e)) }], isError: true });

function setStatus(state, text, title) {
  const el = document.getElementById('webmcp-status');
  if (!el) return;
  el.dataset.state = state;
  el.querySelector('.txt').textContent = text;
  el.title = title || text;
}

function essayIdOf(app, input) {
  const id = (input && input.essay_id) || app.currentId;
  if (!id) throw new Error('当前没有打开作文，请先 open_essay 或传 essay_id');
  return id;
}

async function detailOf(app, id) {
  if (id === app.currentId && app.detail) return app.detail;
  return API.get(`/essays/${encodeURIComponent(id)}`, OPT);
}

function compactDetail(d, sel) {
  const ev = d.evaluations[0];
  return {
    essay: { id: d.essay.id, subject: d.essay.subject, type: d.essay.type, title: d.essay.title, category: d.essay.category, tags: d.essay.tags, prompt: d.essay.prompt, hasPromptImage: !!d.essay.promptImage, wordCount: d.essay.wordCount, content: d.essay.content },
    annotations: d.annotations.filter((a) => a.status !== 'resolved').map((a) => ({ id: a.id, parentId: a.parentId, quote: a.quote, start: a.start, end: a.end, kind: a.kind, label: a.label, note: a.note, suggestion: a.suggestion, author: a.author, status: a.status })),
    latestEvaluation: ev ? { score: ev.score, maxScore: ev.maxScore, band: ev.band, dimensions: ev.dimensions, overallComment: ev.overallComment, createdAt: ev.createdAt } : null,
    versionCount: d.versions.length,
    selection: sel || null,
  };
}

function tools(app) {
  const title = app.detail ? `「${app.detail.essay.title}」` : '（未打开）';
  const withFlush = async (id) => { if (id === app.currentId && app.flush) await app.flush(); };
  const after = async (id) => { if (id === app.currentId) await app.reload(); };
  return [
    {
      name: 'get_current_essay',
      description: `读取用户当前在页面上打开的作文${title}：题目、正文、标注、最新评分，以及用户此刻选中的文字。`,
      inputSchema: { type: 'object', properties: {} },
      execute: async () => {
        if (!app.currentId) return ok('当前没有打开作文。可用 list_essays 查找，再用 open_essay 打开。');
        return ok(compactDetail(app.detail, app.selection ? app.selection() : null));
      },
    },
    {
      name: 'list_essays',
      description: '列出作文（按更新时间倒序），可按科目、题型、标签、关键词筛选。',
      inputSchema: { type: 'object', properties: { subject: { type: 'string', enum: ['english', 'politics'] }, type: { type: 'string' }, tag: { type: 'string' }, query: { type: 'string' } } },
      execute: async (i = {}) => ok(await API.get('/essays' + API.qs({ subject: i.subject, type: i.type, tag: i.tag, q: i.query }), OPT)),
    },
    {
      name: 'open_essay',
      description: '在页面上打开指定作文（切换用户当前视图）。',
      inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
      execute: async (i) => {
        const d = await app.open(i.id);
        return ok(d ? { opened: d.essay.id, title: d.essay.title } : '打开失败');
      },
    },
    {
      name: 'highlight',
      description: '在作文正文上做标注：给出原文 quote（多次出现时用 occurrence 指定第几处，从 1 开始）、类型 kind 与边注 note。纠错（correction）必须带替换文本 suggestion。',
      inputSchema: {
        type: 'object',
        properties: {
          quote: { type: 'string', description: '要标注的原文，须与正文逐字一致' },
          occurrence: { type: 'integer', minimum: 1 },
          kind: { type: 'string', enum: KINDS, description: 'highlight 重点 / good 亮点 / issue 问题 / question 疑问 / correction 纠错' },
          note: { type: 'string', description: '边注正文' },
          suggestion: { type: 'string', description: '替换文本（correction 必填）' },
          essay_id: { type: 'string', description: '缺省为当前作文' },
        },
        required: ['quote', 'kind'],
      },
      execute: async (i) => {
        const id = essayIdOf(app, i);
        await withFlush(id);
        const item = { quote: i.quote, kind: i.kind, note: i.note || '' };
        if (i.occurrence) item.occurrence = i.occurrence;
        if (i.suggestion !== undefined) item.suggestion = i.suggestion;
        const r = await API.post(`/essays/${encodeURIComponent(id)}/annotations`, { items: [item] }, OPT);
        await after(id);
        return ok(r);
      },
    },
    {
      name: 'reply',
      description: '回复一条标注（例如回答用户在边注里提的疑问）。回复显示在原边注下方。',
      inputSchema: { type: 'object', properties: { annotation_id: { type: 'string' }, note: { type: 'string' }, essay_id: { type: 'string' } }, required: ['annotation_id', 'note'] },
      execute: async (i) => {
        const id = essayIdOf(app, i);
        const d = await detailOf(app, id);
        const parent = d.annotations.find((a) => a.id === i.annotation_id);
        if (!parent) throw new Error('找不到标注 ' + i.annotation_id);
        const r = await API.post(`/essays/${encodeURIComponent(id)}/annotations`, { items: [{ parentId: parent.id, kind: parent.kind, note: i.note }] }, OPT);
        await after(id);
        return ok(r);
      },
    },
    {
      name: 'list_annotations',
      description: '列出作文的标注。author 可为 "user"（用户自己的标记）、"agent" 或完整作者名；可按 kind、status 过滤。',
      inputSchema: { type: 'object', properties: { author: { type: 'string' }, kind: { type: 'string', enum: KINDS }, status: { type: 'string', enum: ['open', 'resolved', 'orphaned'] }, essay_id: { type: 'string' } } },
      execute: async (i = {}) => {
        const id = essayIdOf(app, i);
        const d = await detailOf(app, id);
        const list = d.annotations.filter((a) => {
          if (i.author === 'agent' ? !a.author.startsWith('agent:') : i.author && a.author !== i.author) return false;
          if (i.kind && a.kind !== i.kind) return false;
          if (i.status && a.status !== i.status) return false;
          return true;
        });
        return ok(list);
      },
    },
    {
      name: 'apply_suggestion',
      description: '采纳一条纠错标注的替换建议：用 suggestion 替换原文，并把该标注标记为已解决。',
      inputSchema: { type: 'object', properties: { annotation_id: { type: 'string' } }, required: ['annotation_id'] },
      execute: async (i) => {
        if (app.flush) await app.flush();
        const d = await API.post(`/annotations/${encodeURIComponent(i.annotation_id)}/apply`, {}, OPT);
        await after(d.essay.id);
        return ok({ essayId: d.essay.id, wordCount: d.essay.wordCount });
      },
    },
    {
      name: 'submit_evaluation',
      description: '提交一次评分。dimensions 必须恰好含 content/language/structure/convention 四项，每项 {score, feedback}，四项之和等于 score；corrections 会转成纠错标注。先用 get_rubric 读评分细则。',
      inputSchema: {
        type: 'object',
        properties: {
          essay_id: { type: 'string' },
          score: { type: 'number' },
          band: { type: 'string' },
          dimensions: {
            type: 'object',
            properties: Object.fromEntries(['content', 'language', 'structure', 'convention'].map((k) => [k, { type: 'object', properties: { score: { type: 'number' }, feedback: { type: 'string' } }, required: ['score'] }])),
            required: ['content', 'language', 'structure', 'convention'],
          },
          overall_comment: { type: 'string' },
          strengths: { type: 'array', items: { type: 'string' } },
          weaknesses: { type: 'array', items: { type: 'string' } },
          polished: { type: 'string', description: '润色后的全文' },
          corrections: { type: 'array', items: { type: 'object', properties: { quote: { type: 'string' }, occurrence: { type: 'integer' }, corrected: { type: 'string' }, kind: { type: 'string' }, explanation: { type: 'string' } }, required: ['quote', 'corrected', 'kind'] } },
        },
        required: ['score', 'band', 'dimensions'],
      },
      execute: async (i) => {
        const id = essayIdOf(app, i);
        await withFlush(id);
        const body = {
          score: i.score,
          band: i.band || '',
          dimensions: i.dimensions,
          overallComment: i.overall_comment ?? i.overallComment ?? '',
          strengths: i.strengths || [],
          weaknesses: i.weaknesses || [],
          polished: i.polished || '',
          corrections: i.corrections || [],
        };
        const r = await API.post(`/essays/${encodeURIComponent(id)}/evaluations`, body, OPT);
        await after(id);
        return ok({ evaluationId: r.evaluation.id, versionId: r.version.id, corrections: r.corrections });
      },
    },
    {
      name: 'get_rubric',
      description: '读取题型的评分细则、满分、维度权重与字数要求。type 缺省为当前作文的题型。',
      inputSchema: { type: 'object', properties: { type: { type: 'string' } } },
      execute: async (i = {}) => {
        const t = i.type || (app.detail && app.detail.essay.type);
        if (!t) throw new Error('请指定 type');
        return ok(await API.get(`/rubric/${encodeURIComponent(t)}`, OPT));
      },
    },
    {
      name: 'get_prompt',
      description: '取得针对当前作文的工作提示：grade_essay（评分）、polish_essay（润色）、review_my_marks（逐条回应用户标记）。',
      inputSchema: { type: 'object', properties: { name: { type: 'string', enum: ['grade_essay', 'polish_essay', 'review_my_marks'] }, essay_id: { type: 'string' } }, required: ['name'] },
      execute: async (i) => {
        const id = essayIdOf(app, i);
        const r = await API.get(`/prompts/${encodeURIComponent(i.name)}?essay=${encodeURIComponent(id)}`, OPT);
        return ok(r.text);
      },
    },
  ];
}

export function init(app) {
  const mc = document.modelContext ?? navigator.modelContext;
  if (!mc || typeof mc.registerTool !== 'function') {
    setStatus('off', 'WebMCP 未检测到', '浏览器没有原生 WebMCP（document.modelContext）；页面照常可用');
    return;
  }
  let ctrl = null;
  let names = [];
  let running = Promise.resolve();

  const unregister = async (name) => {
    if (typeof mc.unregisterTool === 'function') {
      try { await mc.unregisterTool(name); } catch { /* 不存在 */ }
    }
  };

  const wrap = (t) => ({
    ...t,
    execute: async (input) => {
      try { return await t.execute(input || {}); } catch (e) { return fail(e); }
    },
  });

  const registerOne = async (t, signal) => {
    try {
      await mc.registerTool(wrap(t), { signal });
    } catch (e) {
      if (e && e.name === 'InvalidStateError') {
        await unregister(t.name);
        await new Promise((r) => setTimeout(r, 0));
        await mc.registerTool(wrap(t), { signal });
      } else throw e;
    }
  };

  const registerAll = async () => {
    if (ctrl) ctrl.abort();
    for (const n of names) await unregister(n); // 旧实现不认 signal 时手动注销
    ctrl = new AbortController();
    names = [];
    let okCount = 0, lastErr = null;
    for (const t of tools(app)) {
      try { await registerOne(t, ctrl.signal); names.push(t.name); okCount++; } catch (e) { lastErr = e; }
    }
    if (okCount) setStatus('on', `WebMCP 已启用 · ${okCount} 个工具`, names.join(', '));
    else setStatus('err', 'WebMCP 注册失败', lastErr ? String(lastErr.message || lastErr) : '');
  };

  const schedule = () => { running = running.then(registerAll).catch((e) => setStatus('err', 'WebMCP 注册失败', String(e))); };
  schedule();
  let t;
  window.addEventListener('essaypilot:change', () => { clearTimeout(t); t = setTimeout(schedule, 50); });
}
