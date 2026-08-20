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
```

## WeKnora 接入点

- `GET /api/v1/research/reports`：研究记忆页的演示数据接口，后续可对接机构语义库。
- `POST /api/v1/research/ask`：登录 WeKnora、读取 HYGR投研工作台配置、创建会话、消费 SSE 响应，再返回完整回答与引用；页面的“仅内部 / 内部 + 实时 / 仅原始来源”会作为明确的检索指令传入智能体。
- 智能问答在 WeKnora 未配置或故障时会明确报错，不会把演示内容伪装成生产研究结果。
