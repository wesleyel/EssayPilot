# 接口

EssayPilot 对外有三类接口：给 Agent 用的 **MCP**（`/mcp`，需用 `-mcp` 开启），给界面用的 **REST + SSE**（`/api`），以及页面向浏览器 Agent 注册的 **WebMCP** 工具。三者底层共用同一套业务逻辑，所以无论通过哪一类接口改动数据，界面都会实时刷新。

JSON 形状见 [`internal/core/types.go`](../internal/core/types.go)，枚举（科目、题型、分类、维度、纠错类别、标注类型）见 [`internal/core/model.go`](../internal/core/model.go)。

## 约定

- **作者（actor）**：界面上的操作记为 `user`；通过 MCP 的操作记为 `agent:<客户端名>`，例如 `agent:claude-code`；通过 WebMCP 的操作记为 `agent:webmcp`。界面按类型分边显示标注：左页边是重点与亮点，右页边是问题、疑问与纠错；作者用颜色区分。
- **偏移**：标注的 `start` 和 `end` 以 UTF-16 码元计，左闭右开，与浏览器 `textarea.selectionStart` 的计法一致。Agent 一般不需要自己算偏移，直接给 `quote`（逐字摘自正文的原文），由服务端定位即可。
- **锚定**：正文修改后，标注会跟着原文移动。原文被删掉时，标注变为 `orphaned`，界面在正文底部列出，可以手动重新定位或删除。之后如果原文又出现，标注会自动恢复为 `open`。
- **令牌**：用 `-token` 启动后，`/api` 和 `/mcp` 都需要带 `Authorization: Bearer <token>`；SSE 请求可以改用 `?token=` 传令牌。

## MCP

### Tools

| 工具 | 参数（`?` 表示可选） | 说明 |
| --- | --- | --- |
| `get_ui_context` | | 用户在界面里当前打开的作文，以及选中的文字 |
| `list_types` | | 题型、分类、各科目评分维度、纠错类别、标注类型 |
| `list_essays` | subject?, type?, tag?, query? | 列出作文（不含正文） |
| `get_essay` | id | 题目、正文、全部标注、评分记录、版本，以及按段编号的正文 `numbered` |
| `create_essay` | subject, type, title?, category?, tags, prompt?, content? | 新建作文，至少要有一个主题标签 |
| `update_essay` | id, type?, title?, category?, tags?, prompt?, content? | 只传要改的字段 |
| `delete_essay` | id | 永久删除 |
| `save_version` / `list_versions` / `restore_version` | essay_id, … | 版本快照 |
| `annotate` | essay_id, items[{quote, occurrence?, kind, label?, note?, suggestion?}] | 批量标注；没找到原文的条目会单独报错，不影响其他条目 |
| `reply` | annotation_id, note | 回复一条标注，通常用来回答用户的疑问 |
| `list_annotations` | essay_id, author?, kind?, status? | `author=user` 只列用户自己的标记 |
| `update_annotation` / `delete_annotation` | id, … | |
| `apply_suggestion` / `apply_suggestions` | annotation_id / essay_id, ids? | 采纳纠错，用 suggestion 替换正文 |
| `get_rubric` | type? 或 essay_id? | 阅卷细则 |
| `submit_evaluation` | essay_id, score, band, dimensions, overall_comment, strengths, weaknesses, polished, corrections? | 提交评分；服务端会校验各维度满分以及维度分之和，并自动生成版本快照 |
| `delete_evaluation` | essay_id, evaluation_id | |

标注类型 `kind` 有五种：`highlight` 重点、`good` 亮点、`issue` 问题、`question` 疑问、`correction` 纠错。其中 `correction` 必须带 `suggestion`。重点只画可复用的开头句、结尾句、承接过渡词句和万能句型，`label` 写使用场景、`note` 留空（标准见 `core.HighlightRule`，也可用 prompt `mark_highlights`）。边注 `note` 按 Markdown 渲染（粗体、斜体、删除线、行内代码、链接、列表、引用），不渲染原始 HTML。

### Resources

- `essay://{id}`：作文全文的 markdown，包括标注和最新评分
- `rubric://{type}`：该题型的阅卷细则

### Prompts

- `grade_essay(essay_id)`：评分、纠错、写润色稿，最后调用 `submit_evaluation` 写回
- `polish_essay(essay_id)`：只润色不打分，修改以纠错边注的形式给出
- `review_my_marks(essay_id)`：逐条回应用户做的标记
- `mark_highlights(essay_id)`：按重点标准画出可复用的开头句、结尾句、承接过渡词句和万能句型，`label` 写使用场景

界面底部的「复制 …」按钮会把这些提示词生成好的全文复制到剪贴板，可以直接粘贴给任何 Agent，包括没有接入 MCP 的 Agent（它会按提示词去调用 REST）。

## REST

除 `DELETE` 返回 204 外，其余请求都以 JSON 收发。出错时返回 `{"error": "..."}`，状态码为 400、404 或 500。可选请求头 `X-Actor`，取值为 `user` 或 `agent:*`，用来标明操作者。

| 方法 路径 | 请求 | 响应 |
| --- | --- | --- |
| GET `/api/meta` | | 题型、维度、纠错类别、标注类型、MCP 是否开启 |
| GET `/api/essays?subject=&type=&tag=&q=` | | `EssaySummary[]` |
| POST `/api/essays` | `CreateEssayInput` | `EssayDetail` |
| GET / PATCH / DELETE `/api/essays/{id}` | `UpdateEssayInput` | `EssayDetail` |
| POST `/api/essays/{id}/versions` | `{note?}` | `Version` |
| POST `/api/essays/{id}/versions/{vid}/restore` | | `EssayDetail` |
| DELETE `/api/essays/{id}/versions/{vid}` | | |
| POST `/api/essays/{id}/annotations` | `{items: AnnotateItem[]}` | `AnnotateResult[]` |
| POST `/api/essays/{id}/apply` | `{ids?}` | `EssayDetail` |
| POST `/api/essays/{id}/evaluations` | `EvaluationInput` | `EvaluationResult` |
| DELETE `/api/essays/{id}/evaluations/{eid}` | | |
| PATCH / DELETE `/api/annotations/{aid}` | `AnnotationPatch` | `Annotation` |
| POST `/api/annotations/{aid}/apply` | | `EssayDetail` |
| GET `/api/highlights` | | `HighlightItem[]`：所有作文中仍挂在正文上的重点（顶层 `highlight`、状态 open），每条附作文标题、科目、题型、分类和主题标签 |
| GET `/api/rubric/{type}` | | `{spec, text}` |
| GET `/api/prompts/{name}?essay={id}` | | `{text}` |
| GET / PUT `/api/ui-context` | `UIContext` | `UIContext` |
| GET `/api/activity?limit=` | | `Activity[]` |
| GET `/api/events` | | SSE，事件类型包括 `essay` `annotations` `evaluations` `versions` `deleted` `activity` `ui-context` |

## WebMCP

当浏览器提供 `document.modelContext`（较早的实现是 `navigator.modelContext`）时，页面会注册一组以当前页面为上下文的工具，例如 `get_current_essay`、`highlight`、`reply`、`submit_evaluation` 等，内部都调用上面的 REST 接口。浏览器不支持时页面不受任何影响。这套规范还处在 Chrome 的 origin trial 阶段，接口可能还会变；如需申请 trial，用 `-webmcp-token` 注入令牌。
