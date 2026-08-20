# Research OS

面向腾讯 WeKnora 的机构研究工作台。前端体现研究记忆、智能问答、研究工作区、ETH 风险雷达和管理员配置；Go 服务会调用 WeKnora「机构研究工作台」智能体。

## 运行

```powershell
npm install
npm run dev
```

构建并用 Go 托管：

```powershell
npm run build
go run ./cmd/researchos
```

生产构建时须从待部署的 `HEAD` 注入版本信息，使页面底部与实际二进制保持一致：

```powershell
$commit = git rev-parse --short=12 HEAD
$branch = git branch --show-current
$commitTime = git show -s --format=%cI HEAD
npm run build
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w -X main.buildCommit=$commit -X main.buildBranch=$branch -X main.buildCommitTime=$commitTime" -o build/researchos ./cmd/researchos
```

`GET /api/v1/version` 返回页脚使用的作者、提交时间、提交 ID 与分支；本地 `go run` 会明确显示 `local-dev`。

默认打开 `http://localhost`。健康检查：`http://localhost/health`。如需改用其他端口，可设置 `RESEARCH_OS_ADDR`（例如 `:8080`）。

在启动 Go 服务前，设置 WeKnora 凭据（请只在终端或部署平台的密钥管理中设置，切勿写入仓库）：

```powershell
$env:WEKNORA_BASE_URL = 'http://10.15.0.27'
$env:WEKNORA_EMAIL = 'your-weknora-email'
$env:WEKNORA_PASSWORD = 'your-weknora-password'
# 可选：默认已指向当前的机构研究工作台智能体
# 默认已指向 HYGR投研工作台智能体；如该智能体改版，可在部署环境覆盖。
$env:WEKNORA_AGENT_ID = '30a2f66f-7650-4cb0-a6f8-e64981b8a95d'
# 可选：限定为 HYGR投研工作台当前选定的“机构研究记忆”知识库。
$env:WEKNORA_KNOWLEDGE_BASE_ID = '1006a6d7-5baa-42e0-b0c7-ed7908dbe507'
# 可选：研究工作区“在 WeKnora 中打开”使用的控制台地址；未设置时使用 WEKNORA_BASE_URL。
$env:WEKNORA_CONSOLE_URL = 'http://10.15.0.27'
# 可选：研究工作区单文件上传上限（MB，默认 50）；应与 WeKnora 的 MAX_FILE_SIZE_MB 保持一致。
$env:RAG_UI_UPLOAD_MAX_MB = '50'
```

爆仓气泡图使用 Binance、OKX 的公开行情 WebSocket，不需要交易 API Key。生产环境需为持久化数据配置一个最小权限 PostgreSQL 账号，并仅在部署环境中设置 DSN：

```powershell
$env:RAG_UI_DATABASE_URL = 'postgres://rag_ui_app:password@db-host:5432/RAG-UI?sslmode=disable'
# 可选：共同上架的 USDT 永续合约数量与保留时长（默认 50、7 天）
$env:LIQUIDATION_SYMBOL_LIMIT = '50'
$env:LIQUIDATION_RETENTION_HOURS = '168'
```

未设置 `RAG_UI_DATABASE_URL` 时，服务仍可启动，但爆仓气泡图会显示数据库不可用状态且不会伪造历史数据。

## WeKnora 接入点

- `GET /api/v1/research/reports`：研究记忆页的演示数据接口，后续可对接机构语义库。
- `POST /api/v1/research/ask`：登录 WeKnora、读取 HYGR投研工作台配置、创建会话、消费 SSE 响应，再返回完整回答与引用；页面的“仅内部 / 内部 + 实时 / 仅原始来源”会作为明确的检索指令传入智能体。
- `GET /api/v1/research/uploads`、`POST /api/v1/research/uploads`：固定使用已配置的 WeKnora 知识库列出最近 10 个文件、上传单个 PDF/Word/PPT 文件；凭据和知识库 ID 不会返回浏览器。
- 智能问答在 WeKnora 未配置或故障时会明确报错，不会把演示内容伪装成生产研究结果。
- `GET /api/v1/liquidations/symbols`、`/status`、`/chart` 和 `/stream`：爆仓气泡图的只读目录、采集状态、历史快照与浏览器实时推送接口。
