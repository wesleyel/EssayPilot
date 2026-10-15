# EssayPilot · 考研作文批阅台

考研英语写作与政治主观题的编辑、标注与评分工作台。**服务端不调用模型**：题目的增删改查、润色和评分都由你本地的 Agent（Claude Code、Codex 等）通过 MCP 完成，界面负责写作、标记和查看结果。

**Go 单二进制 · SQLite · MCP（Streamable HTTP）· WebMCP**

## 特性

- **编辑和标记在同一个界面**：选中文字就能标记，类型有重点、亮点、问题、疑问四种，并可附一条边注。边注采用 Tufte 式页边：左侧是重点与亮点，右侧是问题、疑问与纠错（作者用颜色区分），按段落成簇排列；可以一键「全部展开」看每条边注的全文。边注支持 Markdown（粗体、斜体、行内代码、链接、列表、引用）。
- **重点句页**：顶栏「重点句」汇总所有作文里标为「重点」的句子，可搜索，并按作文分类、题型、重点标签、主题标签或作文分组；点一句即跳回原文并展开那条边注。
- **Agent 能读也能写这些标记**：它能读到你的标记和疑问，可以逐条回复，也可以添加纠错标注，纠错可一键采纳。
- **按考研阅卷标准评分**：英语和政治分别有四个评分维度，按档次给分，评分细则通过 MCP 提供给 Agent。每次评分会自动保存一个版本快照。
- **版本与对照**：版本以时间线排列，任意两个版本之间、或当前稿与润色稿之间，都能做词级 diff，也能整体采纳为新版本。
- **实时同步**：Agent 的改动通过 SSE 即时出现在界面上，底部栏显示 Agent 最近调用了哪些工具，以及它当前能看到的选区。
- **WebMCP**：浏览器支持 `document.modelContext` 时，页面会把工具注册给浏览器内置的 Agent。

## 快速开始

```bash
go install ./cmd/essaypilot   # 装到 $(go env GOPATH)/bin；或 go build -o <目录> ./cmd/essaypilot
essaypilot -mcp              # http://127.0.0.1:8790 ，MCP 在 /mcp
```

接入 Claude Code：

```bash
claude mcp add --transport http essaypilot http://127.0.0.1:8790/mcp
```

然后可以对 Agent 说「给我正在看的这篇评分」，或者在界面底部点「复制 grade_essay」，把提示词粘贴给任意一个 Agent。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8790` | 监听地址 |
| `-db` | `essaypilot.db` | 数据文件，备份时直接复制这个文件即可 |
| `-mcp` | 关 | 开启 `/mcp` |
| `-token` | `$ESSAYPILOT_TOKEN` | 访问令牌。监听非本机地址时必须设置；在浏览器里用 `http://host:port/#token=<令牌>` 打开 |
| `-webmcp-token` | 空 | Chrome WebMCP origin trial 令牌 |

需要 Go 1.26 及以上。SQLite 驱动是纯 Go 实现，`CGO_ENABLED=0` 也能交叉编译。

## Agent 能做什么

MCP 工具、资源和提示词的完整说明见 [docs/api.md](docs/api.md)。一次典型的评分流程：

1. `get_ui_context`：看用户当前打开的是哪一篇、选中了什么
2. `get_rubric` → `get_essay`：读取评分细则和正文
3. `submit_evaluation`：提交总分、四维分、总评、润色稿，以及纠错。纠错会作为边注挂到原文上
4. `annotate` / `reply`：补充标注，或回答用户的疑问

## 结构

```
cmd/essaypilot/   入口：启动参数、组装服务
internal/core/    领域与业务层
  model.go        科目、题型、分类、评分维度、纠错类别
  types.go        实体与输入类型（REST、MCP、前端共用的 JSON 形状）
  rubric.go       阅卷细则（以 MCP 资源和提示词的形式提供给 Agent）
  prompts.go      grade_essay / polish_essay / review_my_marks / mark_highlights
  store.go        SQLite
  anchor.go       标注锚定：正文修改后，标注跟着原文移动
  service.go      业务层（REST 与 MCP 共用）
  events.go       SSE 事件与 Agent 动态
internal/server/  HTTP 层
  server.go       路由组装、静态资源、令牌
  api.go          REST + SSE
  mcp.go          MCP 服务
web/              前端（原生 JS，embed.go 用 go:embed 打包进二进制）
```

## 许可

[MIT](LICENSE)
