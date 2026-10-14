---
name: essay-eval-api
description: 通过 EssayPilot 的 REST API 增删改查题目与作答、打分类与主题标签、保存版本快照、发起 AI 评分/启发/语料提取。当需要以编程方式读写作文数据、批量改标签或触发批改时使用。
---

# EssayPilot API

API 由 Cloudflare Worker 提供（`apps/api`），路径前缀 `/api`，JSON 收发，**无鉴权**——只在本机或受信网络使用。
类型定义见 `packages/domain/src`；错误统一返回 `{ "error": "..." }`（400 校验、404 不存在、502 模型出错）。

本地：`pnpm dev` 后为 `http://127.0.0.1:8787/api`（本地库默认为空，可先 `pnpm db:migrate && pnpm db:seed`）；自托管时换成你的 Worker 地址 + `/api`。
GitHub Pages 的纯浏览器模式没有 HTTP API，本 skill 不适用。

## 枚举

- `subject`：`english` | `politics`
- `type`：english → `part-a`（小作文 10 分）、`part-b`（大作文 20 分）；politics → `mayuan` `maozhongte` `shigang` `defa` `dangdai`。type 必须属于 subject，subject 创建后不可改。

## 题目/作答（essay）

| 操作 | 请求 | 说明 |
|---|---|---|
| 列表 | `GET /essays?subject=&type=` | 摘要，不含图片，带最新评分与计数 |
| 详情 | `GET /essays/:id` | `{ essay, evaluations, versions, inspiration }`，新的在前 |
| 创建 | `POST /essays` | `{ subject, type, category, tags, title?, prompt?, promptImage?, content? }`，201 |
| 更新 | `PATCH /essays/:id` | 可改 `type title category tags prompt promptImage content`，其他字段会被拒绝 |
| 删除 | `DELETE /essays/:id` | 204，级联删除版本/评分/启发；其语料保留，来源置空 |

- `prompt` 是题目要求，`content` 是作答正文；`promptImage` 为 `data:image/...;base64,`，需小于约 1.2MB。字数由服务端计算（英文按词，政治按字）。

### 分类 `category`

题型之下的一级归类，各题型互不相交，题型与分类不匹配返回 400；PATCH 改 `type` 时必须同时给出新题型下的 `category`。

- `part-a`：`建议信` `推荐信` `邀请信` `致歉信` `申请求助信` `咨询回复` `通知`
- `part-b`：`图画·人生态度` `图画·品德情感` `图画·教育成长` `图画·文化社会` `图表·柱状图` `图表·表格` `图表·组合图`
- 政治：无分类，`category` 必须为空串

### 主题标签 `tags`

1–10 个，每个不超过 20 字，服务端去空白去重；PATCH 会**整体替换**，不能清空。以下命名约定服务端不校验，打标签时必须遵守，并优先复用库里已有的标签（先 `GET /essays` 看现有标签）：

- **英语**：反映写法走向与语料，3–6 个，按「技法 → 情感 → 走向 → 主题」排列。
  - 大作文技法：`反差对比` `同类比较`（饼图/柱状图各项排序） `趋势变化` `多项同增` `图文互证` `寓意隐喻` `正反两面` `原因分析` `让步反驳`
  - 大作文情感：`积极赞扬` `理性审慎` `褒贬对照` `温情关怀`；走向：`呼吁倡导` `条件展望`（Only when…） `前景展望` `哲理收尾`
  - 小作文：`约定时间地点` `主动帮忙` `分点建议` `中外对比` `文化介绍` `推荐地点`/`推荐作品`/`推荐资料` `礼仪提醒` `活动介绍` `报名方式` `诚挚邀请` `承诺支持` `鼓励打气` `表达祝愿` `欢迎到访`
  - 末尾一个语料主题：如 `传统文化` `科技AI` `绿色出行` `文旅体验` `青年成长` `城市民生`
  - 例：`["反差对比","褒贬对照","让步反驳","哲理收尾","文旅体验"]`
  - 还没有正文时按题目要求预判写法，写完后再校正。
- **政治**：一律「板块-知识点」，板块取 `马哲` `政经` `毛概` `新思想` `史纲` `德法` `当代`；题目跨板块时可带其他板块的标签。例：`["马哲-矛盾","马哲-矛盾分析法","新思想-生态文明"]`、`["政经-资本有机构成","马哲-生产力"]`
- 不打来源或重复类标签：`2026真题` `第34题` `预测题` `Paul来信` `仿写练习` `电子邮件`，以及与题型重复的 `马原` `习思想`（这些信息放在标题里）。

## 版本快照

- `POST /essays/:id/versions` `{ note? }` → 保存当前作答快照，201
- `DELETE /essays/:id/versions/:versionId` → 删除版本（其评分一并删除）
- `DELETE /essays/:id/evaluations/:evaluationId` → 只删评分，保留版本

## AI 操作（需先在设置里配置模型）

- `POST /essays/:id/evaluations` → 评分，返回 `{ evaluation, version }`，并自动生成版本快照。`content` 为空返回 400。耗时数十秒，设置长超时。
- `POST /essays/:id/inspiration` → 表达启发（每篇只保留最新一次）
- `POST /essays/:id/title` → `{ title }`，只生成建议不写入；需要时自行 `PATCH`
- `POST /templates/extract` `{ subject, essayId? }` → 从作答提取句式/金句入库，201，返回新增条目。省略 `essayId` 则取该科目全部有正文的作答。**每篇作答只保留最新一次提取**：写入前会删掉这些作答之前提取的句式；与其他作答的句式文字完全相同时跳过。

`evaluation` 关键字段：`score maxScore band dimensions overallComment strengths weaknesses corrections[] polished source createdAt`。

## 语料库

- `GET /templates?subject=` → 每条带 `source: { essayId, title, type, category, tags }`（来源作答的分类与主题，语料库据此做两级筛选；来源已删除或与语料不同科目时为 null）
- `DELETE /templates/:id` → 204

## 设置与备份

- `GET /settings` → `{ baseUrl, model, reasoningEffort, apiKeySet }`（不返回密钥）
- `PUT /settings` `{ baseUrl, model, reasoningEffort: auto|none|low|medium|high, apiKey? }`：省略 apiKey 沿用旧值，空串清除
- `POST /settings/test`（同 PUT 的 body）→ `{ ok, message }`
- `GET /backup` → `{ exportedAt, essays: [{ essay, evaluations, versions, inspiration }], templates }`

## 典型流程

```bash
B=http://127.0.0.1:8787/api
curl -s $B/backup > backup-$(date +%Y%m%d-%H%M).json   # 批量改动前先备份
ID=$(curl -s -X POST $B/essays -H 'content-type: application/json' -d '{
  "subject":"english","type":"part-b","category":"图画·人生态度",
  "tags":["反差对比","褒贬对照","呼吁倡导","文旅体验"],
  "prompt":"题目...","content":"作文..."}' | jq -r .id)
curl -s -X POST $B/essays/$ID/evaluations --max-time 180 | jq '.evaluation | {score,maxScore,band}'
curl -s -X PATCH $B/essays/$ID -H 'content-type: application/json' -d '{"tags":["反差对比","褒贬对照","哲理收尾","文旅体验"]}'
```

## 注意

- 改动作答后评分不会自动重跑；对比提分请再次 `POST /evaluations`，历史在 `versions`/`evaluations` 中。
- 删除不可恢复；批量改动前先 `GET /backup`。
