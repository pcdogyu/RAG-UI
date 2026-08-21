import { useCallback, useEffect, useRef, useState } from 'react'
import * as echarts from 'echarts/core'
import { CandlestickChart, ScatterChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { navGroups, reports } from './data'
import { renderResearchAnswerHtml } from './researchAnswerHtml'

echarts.use([CandlestickChart, ScatterChart, DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer])

type Page = typeof navGroups[number]['items'][number][0]
type Theme = 'light' | 'dark'
const themeStorageKey = 'research-os-theme'
const storedTheme = (): Theme => {
  try { return localStorage.getItem(themeStorageKey) === 'light' ? 'light' : 'dark' } catch { return 'dark' }
}
const pageMeta: Record<Page, [string, string]> = {
  ask: ['智能研究问答', '带着整个团队的研究记忆，与当下市场对话'],
  memory: ['机构研究记忆', '将报告、证据与投资判断沉淀为可调用的机构资产'],
  studio: ['研究工作区', '上传、校验与裁决，让 AI 辅助而不替代研究责任'],
  risk: ['ETH 风险雷达', '可解释的多维风险共振监测 · 不连接自动交易'],
  liquidationMap: ['清算地图', 'ETH 30 日清算热区 · 外部研究监测页面'],
  liquidation: ['爆仓气泡图', 'Binance / OKX 公开清算流 · 仅研究监测，不连接交易'],
  watchlist: ['重点关注池', '集中查看标的、主题与持仓的研究状态'],
  cockpit: ['研究驾驶舱', '面向团队的研究状态聚合 · 内容成熟后逐步启用'],
  sources: ['知识库与数据接入', '管理 WeKnora 知识库与已授权的数据同步源'],
  rules: ['风险规则配置', '调整阈值与提醒范围，所有修改均保留审计记录'],
  audit: ['成员权限与审计', '研究数据的访问边界与关键操作留痕'],
}

const Pill = ({ children, tone = 'neutral' }: { children: React.ReactNode, tone?: string }) => <span className={`pill ${tone}`}>{children}</span>
const Card = ({ children, className = '' }: { children: React.ReactNode, className?: string }) => <section className={`card ${className}`}>{children}</section>

type VersionInfo = { author: string, commit_time: string, commit_id: string, branch: string }
type ToolCall = { name: string, detail: string, source: 'agent' | 'gateway', started_at: string, duration_ms: number, status: 'completed' | 'failed' }
type Citation = { id: string, title: string, url?: string, source?: 'internal' | 'external', chunk_id?: string }

function BuildFooter() {
  const [version, setVersion] = useState<VersionInfo | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  useEffect(() => {
    let active = true
    void fetch('/api/v1/version', { cache: 'no-store' })
      .then(response => response.ok ? response.json() as Promise<VersionInfo> : Promise.reject(new Error('version request failed')))
      .then(value => { if (active) setVersion(value) })
      .catch(() => { if (active) setUnavailable(true) })
    return () => { active = false }
  }, [])
  if (unavailable) return <footer className="build-footer">版本信息不可用</footer>
  if (!version) return <footer className="build-footer">版本信息加载中…</footer>
  const commitTime = version.commit_time.replace(/(?:Z|[+-]\d{2}:\d{2})$/, '')
  return <footer className="build-footer">Code by {version.author} · {commitTime} · {version.commit_id} · {version.branch}</footer>
}

function AskPage() {
  const scopeOptions = [{ value: '仅内部', label: '仅内部' }, { value: '内部 + 实时', label: '内部+实时' }, { value: '实时', label: '实时' }]
  const [scope, setScope] = useState('内部 + 实时')
  const [question, setQuestion] = useState('')
  const [answer, setAnswer] = useState<string | null>(null)
  const [citations, setCitations] = useState<Citation[]>([])
  const [toolCalls, setToolCalls] = useState<ToolCall[]>([])
  const [answerSource, setAnswerSource] = useState('')
  const [answerScope, setAnswerScope] = useState('')
  const [answeredQuestion, setAnsweredQuestion] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const submit = async () => {
    const normalizedQuestion = question.trim()
    if (!normalizedQuestion || isLoading) return
    setIsLoading(true)
    setError(null)
    setAnswer(null)
    setCitations([])
    setToolCalls([])
    setAnswerSource('')
    setAnswerScope('')
    try {
      const response = await fetch('/api/v1/research/ask', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ question: normalizedQuestion, scope }) })
      const result = await response.json().catch(() => null) as { error?: string, source?: string, answer?: { conclusion?: string, citations?: Citation[], tool_calls?: ToolCall[] } } | null
      if (!response.ok || !result?.answer?.conclusion) throw new Error(result?.error || 'HYGR 未返回可展示的研究回答，请稍后重试。')
      setAnswer(result.answer.conclusion)
      setCitations(result.answer.citations ?? [])
      setToolCalls(result.answer.tool_calls ?? [])
      setAnswerScope(scope)
      setAnsweredQuestion(normalizedQuestion)
      setAnswerSource(result.source === 'weknora-agent' ? 'HYGR 智能问答' : '研究服务')
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '当前无法连接 HYGR 智能问答，请稍后重试。')
    } finally {
      setIsLoading(false)
    }
  }
  const internalCitations = citations.filter(citation => (citation.source ?? 'internal') === 'internal')
  const externalCitations = citations.filter(citation => citation.source === 'external')
  return <div className="ask-layout">
    <Card className="ask-hero">
      <div className="eyebrow">ASK RESEARCH <span>WEKNORA RETRIEVAL READY</span></div>
      <h2>今天，想验证什么判断？</h2>
      <p>答案会区分历史观点与当前信息，并保留每条证据的时间锚点。</p>
      <div className="ask-connection"><i /> HYGR 智能问答已连接 · 当前页直接检索与回答</div>
      <div className="scope-row">{scopeOptions.map(item => <button key={item.value} disabled={isLoading} onClick={() => setScope(item.value)} className={`scope ${scope === item.value ? 'active' : ''}`}>{item.label}</button>)}</div>
      <div className="ask-box"><input aria-label="研究问题" disabled={isLoading} placeholder="输入需要验证的研究判断…" value={question} onChange={e => { setQuestion(e.target.value); setError(null) }} onKeyDown={e => e.key === 'Enter' && void submit()} /><button disabled={isLoading || !question.trim()} onClick={() => void submit()}>{isLoading ? '检索中…' : '发起研究 →'}</button></div>
      <div className="quick-row">{['问财报', '问事件', '问标的', '问持仓', '问历史'].map(x => <button disabled={isLoading} key={x} onClick={() => { setQuestion(`${x}：请给出当前最重要的研究结论`); setError(null) }}>{x}</button>)}</div>
    </Card>
    {isLoading && <Card className="answer-card answer-loading"><div className="answer-loading-indicator" /><div><b>正在检索 HYGR 投研工作台…</b><p>正在结合所选范围内的研究记忆与市场数据生成回答。</p></div></Card>}
    {error && <Card className="answer-card answer-error"><b>本次研究未完成</b><p>{error}</p><button className="primary" onClick={() => void submit()}>重新发起研究</button></Card>}
    {answer && !isLoading && <Card className="answer-card">
      <div className="answer-top"><div><Pill tone="violet">{answerScope}</Pill><span className="temporal">{answerSource}</span></div></div>
      <div className="question-label">{answeredQuestion}</div>
      <div className="answer-content"><h4>研究回答</h4><div className="answer-html" dangerouslySetInnerHTML={{ __html: renderResearchAnswerHtml(answer) }} /></div>
      <section className="citations"><span>检索引用</span>{citations.length ? <div className="citation-groups">{internalCitations.length > 0 && <div className="internal-citations"><label>内部知识库</label>{internalCitations.map((citation, index) => <article className="internal-citation" key={`${citation.id}-${index}`}><span>▣</span><div><b>{citation.title}</b>{citation.chunk_id && <small>片段 {citation.chunk_id.slice(0, 8)}</small>}</div></article>)}</div>}{externalCitations.length > 0 && <div className="external-citations"><label>外部实时来源</label>{externalCitations.map((citation, index) => citation.url ? <a key={`${citation.id}-${index}`} href={citation.url} target="_blank" rel="noreferrer">{citation.title} ↗</a> : <span className="citation-chip" key={`${citation.id}-${index}`}>{citation.title}</span>)}</div>}</div> : <span className="no-citation">本次回答未返回可展示的引用。</span>}</section>
      <details className="tool-history" open>
        <summary>工具调用历史 <span>{toolCalls.length} 步</span></summary>
        <p className="tool-history-note">优先展示 WeKnora 智能体实际执行的工具，再记录本服务的调用步骤；不展示令牌、密码、会话 ID、工具参数或问题原文。</p>
        {toolCalls.length ? <ol>{toolCalls.map((call, index) => <li key={`${call.name}-${call.started_at}-${index}`}><span className={`tool-call-status ${call.status}`} /><div><b>{call.name}</b><em>{call.source === 'agent' ? '智能体工具' : '服务步骤'}</em><p>{call.detail}</p></div><time>{new Date(call.started_at).toLocaleTimeString()} · {call.duration_ms} ms</time></li>)}</ol> : <p className="tool-history-empty">本次回答未返回调用记录。</p>}
      </details>
    </Card>}
  </div>
}

type MemoryScope = 'internal' | 'internal_live'
type MemoryCapability = { id: string, title: string, description: string, enabled: boolean }
type MemoryAgent = { name: string, description: string, avatar: string, capabilities: MemoryCapability[] }
type MemoryAnswer = { conclusion: string, citations: Citation[], tool_calls: ToolCall[] }
type MemoryMessage = { role: 'user' | 'assistant', content: string, answer?: MemoryAnswer }
type MemoryLocalSession = { id: string, title: string, updated_at: string }

const memorySessionsStorageKey = 'research-os-memory-agent-sessions-v1'
const memoryScopeOptions: { value: MemoryScope, label: string, hint: string }[] = [
  { value: 'internal', label: '仅内部资料', hint: '仅检索已入库的机构研究材料' },
  { value: 'internal_live', label: '内部 + 实时网页', hint: '以内部资料为主，并补充实时公开网页' },
]

function readMemorySessions(): MemoryLocalSession[] {
  try {
    const raw = JSON.parse(localStorage.getItem(memorySessionsStorageKey) ?? '[]')
    return Array.isArray(raw) ? raw.filter((item): item is MemoryLocalSession => typeof item?.id === 'string' && typeof item?.title === 'string' && typeof item?.updated_at === 'string').slice(0, 20) : []
  } catch { return [] }
}

function memorySessionTitle(question: string) {
  const compact = question.replace(/\s+/g, ' ').trim()
  return compact.length > 28 ? `${compact.slice(0, 28)}…` : compact || '新研究记忆'
}

function MemoryCitations({ citations }: { citations: Citation[] }) {
  const internal = citations.filter(citation => (citation.source ?? 'internal') === 'internal')
  const external = citations.filter(citation => citation.source === 'external')
  if (!citations.length) return <span className="no-citation">本次回答未返回可展示的引用。</span>
  return <div className="citation-groups">{internal.length > 0 && <div className="internal-citations"><label>内部知识库</label>{internal.map((citation, index) => <article className="internal-citation" key={`${citation.id}-${index}`}><span>▣</span><div><b>{citation.title}</b>{citation.chunk_id && <small>片段 {citation.chunk_id.slice(0, 8)}</small>}</div></article>)}</div>}{external.length > 0 && <div className="external-citations"><label>外部实时来源</label>{external.map((citation, index) => citation.url ? <a key={`${citation.id}-${index}`} href={citation.url} target="_blank" rel="noreferrer">{citation.title} ↗</a> : <span className="citation-chip" key={`${citation.id}-${index}`}>{citation.title}</span>)}</div>}</div>
}

function MemoryPage() {
  const [agent, setAgent] = useState<MemoryAgent | null>(null)
  const [sessions, setSessions] = useState<MemoryLocalSession[]>(readMemorySessions)
  const [activeSession, setActiveSession] = useState<string | null>(() => readMemorySessions()[0]?.id ?? null)
  const [messages, setMessages] = useState<MemoryMessage[]>([])
  const [question, setQuestion] = useState('')
  const [scope, setScope] = useState<MemoryScope>('internal')
  const [loadingAgent, setLoadingAgent] = useState(true)
  const [loadingMessages, setLoadingMessages] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const persistSessions = useCallback((next: MemoryLocalSession[]) => {
    const bounded = next.slice(0, 20)
    setSessions(bounded)
    try { localStorage.setItem(memorySessionsStorageKey, JSON.stringify(bounded)) } catch { /* Browser privacy mode may block local history. */ }
  }, [])

  const loadAgent = useCallback(async () => {
    setLoadingAgent(true)
    try {
      const response = await fetch('/api/v1/research/memory-agent', { cache: 'no-store' })
      const result = await response.json().catch(() => null) as { agent?: MemoryAgent, error?: string } | null
      if (!response.ok || !result?.agent) throw new Error(result?.error || '研究记忆智能体未连接')
      setAgent(result.agent); setError('')
    } catch (requestError) {
      setAgent(null); setError(requestError instanceof Error ? requestError.message : '研究记忆智能体未连接')
    } finally { setLoadingAgent(false) }
  }, [])

  const loadSession = useCallback(async (sessionID: string) => {
    setLoadingMessages(true)
    try {
      const response = await fetch(`/api/v1/research/memory-agent/sessions/${encodeURIComponent(sessionID)}`, { cache: 'no-store' })
      const result = await response.json().catch(() => null) as { messages?: MemoryMessage[], error?: string } | null
      if (!response.ok) throw new Error(result?.error || '无法读取研究记忆会话')
      setMessages(result?.messages ?? []); setError('')
    } catch (requestError) {
      setMessages([]); setError(requestError instanceof Error ? requestError.message : '无法读取研究记忆会话')
    } finally { setLoadingMessages(false) }
  }, [])

  useEffect(() => { void loadAgent() }, [loadAgent])
  useEffect(() => { if (activeSession) void loadSession(activeSession); else setMessages([]) }, [activeSession, loadSession])

  const createNewSession = () => { if (submitting) return; setActiveSession(null); setMessages([]); setQuestion(''); setError('') }
  const submit = async () => {
    const normalizedQuestion = question.trim()
    if (!normalizedQuestion || submitting || !agent) return
    setSubmitting(true); setError('')
    let sessionID = activeSession
    try {
      if (!sessionID) {
        const created = await fetch('/api/v1/research/memory-agent/sessions', { method: 'POST' })
        const creation = await created.json().catch(() => null) as { session_id?: string, error?: string } | null
        if (!created.ok || !creation?.session_id) throw new Error(creation?.error || '无法新建研究记忆会话')
        sessionID = creation.session_id
        const createdAt = new Date().toISOString()
        persistSessions([{ id: sessionID, title: memorySessionTitle(normalizedQuestion), updated_at: createdAt }, ...sessions.filter(item => item.id !== sessionID)])
      }
      const response = await fetch(`/api/v1/research/memory-agent/sessions/${encodeURIComponent(sessionID)}/messages`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ question: normalizedQuestion, scope }) })
      const result = await response.json().catch(() => null) as { error?: string } | null
      if (!response.ok) throw new Error(result?.error || '研究记忆智能体未返回回答')
      const updatedAt = new Date().toISOString()
      persistSessions([{ id: sessionID, title: memorySessionTitle(normalizedQuestion), updated_at: updatedAt }, ...sessions.filter(item => item.id !== sessionID)])
      setQuestion('')
      setActiveSession(sessionID)
      await loadSession(sessionID)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '研究记忆智能体暂时无法响应，请稍后重试')
    } finally { setSubmitting(false) }
  }

  const activeLabel = sessions.find(item => item.id === activeSession)?.title ?? '新研究记忆'
  return <div className="memory-agent-layout">
    <Card className="memory-agent-hero">
      {loadingAgent ? <div className="memory-agent-loading"><span className="answer-loading-indicator" />正在连接 HYGR 研究记忆…</div> : agent ? <><div className="memory-agent-title"><div className="memory-agent-avatar">{agent.avatar}</div><div><div className="eyebrow">WEKNORA · RESEARCH MEMORY AGENT</div><h2>{agent.name}</h2><p>{agent.description}</p></div><Pill tone="green">● 已连接</Pill></div><div className="memory-capabilities">{agent.capabilities.map(capability => <article key={capability.id} className={capability.enabled ? 'enabled' : 'disabled'}><div><b>{capability.title}</b><p>{capability.description}</p></div><span>{capability.enabled ? '已启用' : '未启用'}</span></article>)}</div></> : <div className="memory-agent-unavailable"><b>研究记忆智能体未连接</b><p>{error || '请联系管理员完成 WeKnora Agent 配置。'}</p><button className="primary" onClick={() => void loadAgent()}>重新连接</button></div>}
    </Card>
    {agent && <div className="memory-workbench">
      <Card className="memory-session-list"><div className="memory-session-head"><div><div className="eyebrow">PRIVATE BROWSER HISTORY</div><b>研究会话</b></div><button className="primary" disabled={submitting} onClick={createNewSession}>+ 新建会话</button></div><p>仅保存在当前浏览器，不展示团队其他成员的会话。</p><div className="memory-session-items">{sessions.length ? sessions.map(session => <button className={session.id === activeSession ? 'active' : ''} key={session.id} disabled={submitting} onClick={() => setActiveSession(session.id)}><b>{session.title}</b><small>{new Date(session.updated_at).toLocaleString()}</small></button>) : <div className="memory-session-empty">从一个问题开始建立研究记忆。</div>}</div></Card>
      <Card className="memory-chat"><div className="memory-chat-head"><div><Pill tone="violet">{activeSession ? '记忆会话' : '新会话'}</Pill><h2>{activeLabel}</h2></div><span>回答保留真实引用与检索痕迹</span></div>{loadingMessages ? <div className="memory-agent-loading"><span className="answer-loading-indicator" />正在回读研究记忆…</div> : messages.length ? <div className="memory-message-list">{messages.map((message, index) => message.role === 'user' ? <article className="memory-message user" key={`user-${index}`}><label>研究员</label><p>{message.content}</p></article> : <article className="memory-message assistant" key={`assistant-${index}`}><label>{agent.name}</label><div className="answer-html" dangerouslySetInnerHTML={{ __html: renderResearchAnswerHtml(message.content) }} />{message.answer && <><section className="citations"><span>检索引用</span><MemoryCitations citations={message.answer.citations ?? []} /></section><details className="tool-history"><summary>工具调用历史 <span>{message.answer.tool_calls?.length ?? 0} 步</span></summary>{message.answer.tool_calls?.length ? <ol>{message.answer.tool_calls.map((call, callIndex) => <li key={`${call.name}-${call.started_at}-${callIndex}`}><span className={`tool-call-status ${call.status}`} /><div><b>{call.name}</b><em>{call.source === 'agent' ? '智能体工具' : '服务步骤'}</em><p>{call.detail}</p></div><time>{new Date(call.started_at).toLocaleTimeString()} · {call.duration_ms} ms</time></li>)}</ol> : <p className="tool-history-empty">本次回答未返回调用记录。</p>}</details></>}</article>)}</div> : <div className="memory-chat-empty"><span>◈</span><h3>从机构研究记忆开始</h3><p>提问后可继续追溯观点、证据与时间锚点。选择“内部 + 实时网页”时，回答会区分实时补充来源。</p></div>}{error && <div className="memory-chat-error" role="alert">{error}</div>}<div className="memory-composer"><div className="scope-row">{memoryScopeOptions.map(option => <button title={option.hint} className={`scope ${scope === option.value ? 'active' : ''}`} disabled={submitting} key={option.value} onClick={() => setScope(option.value)}>{option.label}</button>)}</div><div className="memory-input"><textarea aria-label="研究记忆问题" disabled={submitting} placeholder="问一段研究记忆，例如：ETH 中期命题的证据与失效条件是什么？" value={question} onChange={event => { setQuestion(event.target.value); setError('') }} onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submit() } }} /><button disabled={submitting || !question.trim()} onClick={() => void submit()}>{submitting ? '检索中…' : '发送 →'}</button></div><div className="quick-row">{['追溯观点版本', '定位关键证据', '检查失效条件', '梳理下一验证项'].map(item => <button disabled={submitting} key={item} onClick={() => setQuestion(`${item}：请基于机构研究记忆给出可追溯的结论。`)}>{item}</button>)}</div></div></Card>
    </div>}
  </div>
}

type ResearchUpload = { id: string, name: string, file_type: string, size: number, created_at: string, parse_status: string, error_message?: string }
type ResearchUploadsResponse = { uploads?: ResearchUpload[], console_url?: string, error?: string }

const researchUploadAccept = '.pdf,.doc,.docx,.ppt,.pptx'
const formatUploadSize = (size: number) => size >= 1024 * 1024 ? `${(size / 1024 / 1024).toFixed(1)} MB` : `${Math.max(0, Math.round(size / 1024))} KB`
const uploadStatus = (status: string) => ({
  pending: ['等待处理', 'muted'], processing: ['处理中', 'violet'], finalizing: ['整理索引中', 'violet'], completed: ['已入库', 'green'], failed: ['处理失败', 'red'], cancelled: ['已取消', 'amber'],
}[status] ?? ['等待处理', 'muted'])
const isUploadProcessing = (status: string) => ['pending', 'processing', 'finalizing'].includes(status)

function StudioPage() {
  const input = useRef<HTMLInputElement>(null); const [decision, setDecision] = useState<string | null>(null); const [uploads, setUploads] = useState<ResearchUpload[]>([]); const [consoleURL, setConsoleURL] = useState(''); const [loading, setLoading] = useState(true); const [uploadingName, setUploadingName] = useState(''); const [error, setError] = useState(''); const [dragging, setDragging] = useState(false)
  const loadUploads = useCallback(async (quiet = false, signal?: AbortSignal) => {
    if (!quiet) setLoading(true)
    try {
      const response = await fetch('/api/v1/research/uploads', { signal })
      const result = await response.json().catch(() => null) as ResearchUploadsResponse | null
      if (!response.ok) throw new Error(result?.error || '无法读取 WeKnora 上传记录')
      setUploads(result?.uploads ?? []); setConsoleURL(result?.console_url ?? ''); setError('')
    } catch (requestError) {
      if ((requestError as Error).name !== 'AbortError') setError(requestError instanceof Error ? requestError.message : '无法读取 WeKnora 上传记录')
    } finally {
      if (!quiet && !signal?.aborted) setLoading(false)
    }
  }, [])
  useEffect(() => { const controller = new AbortController(); void loadUploads(false, controller.signal); return () => controller.abort() }, [loadUploads])
  const hasProcessing = uploads.some(upload => isUploadProcessing(upload.parse_status))
  useEffect(() => { if (!hasProcessing) return; const timer = window.setInterval(() => { void loadUploads(true) }, 10_000); return () => window.clearInterval(timer) }, [hasProcessing, loadUploads])
  const uploadFile = async (file?: File) => {
    if (!file || uploadingName) return
    const extension = `.${file.name.split('.').pop()?.toLowerCase() ?? ''}`
    if (!researchUploadAccept.split(',').includes(extension)) { setError('仅支持 PDF、Word 或 PPT 文件'); return }
    setUploadingName(file.name); setError('')
    try {
      const payload = new FormData(); payload.append('file', file)
      const response = await fetch('/api/v1/research/uploads', { method: 'POST', body: payload })
      const result = await response.json().catch(() => null) as { error?: string } | null
      if (!response.ok) throw new Error(result?.error || '文件未能提交至 WeKnora 知识库')
      await loadUploads(true)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '文件未能提交至 WeKnora 知识库')
    } finally {
      setUploadingName(''); if (input.current) input.current.value = ''
    }
  }
  const dropFile = (event: React.DragEvent<HTMLDivElement>) => { event.preventDefault(); setDragging(false); void uploadFile(event.dataTransfer.files[0]) }
  return <div className="two-columns"><Card><div className="card-heading"><div><div className="eyebrow">WEKNORA · INGESTION QUEUE</div><h2>待处理研究材料</h2></div><div className="studio-actions"><button className="ghost studio-link" type="button" disabled={!consoleURL} onClick={() => window.open(consoleURL, '_blank', 'noopener,noreferrer')}>在 WeKnora 中打开 ↗</button><button className="primary" type="button" disabled={Boolean(uploadingName)} onClick={() => input.current?.click()}>+ 上传报告</button></div></div><div className={`upload-drop ${dragging ? 'dragging' : ''} ${uploadingName ? 'uploading' : ''}`} role="button" tabIndex={0} aria-label="上传至 WeKnora 知识库" onClick={() => !uploadingName && input.current?.click()} onKeyDown={event => { if ((event.key === 'Enter' || event.key === ' ') && !uploadingName) { event.preventDefault(); input.current?.click() } }} onDragEnter={event => { event.preventDefault(); setDragging(true) }} onDragOver={event => event.preventDefault()} onDragLeave={() => setDragging(false)} onDrop={dropFile}><input ref={input} className="upload-input" type="file" accept={researchUploadAccept} onChange={event => void uploadFile(event.target.files?.[0])} /><span className="upload-mark">⌁</span><b>{uploadingName ? `正在提交 ${uploadingName}` : '拖放或选择 PDF / Word / PPT'}</b><span>{uploadingName ? 'WeKnora 正在接收文件…' : '文件将进入 WeKnora 知识库，结构化解析在后台进行'}</span></div>{error && <div className="upload-error" role="alert">{error}</div>}<div className="upload-history-heading"><b>最近上传文件</b><span>当前 WeKnora 知识库 · 最多 10 条</span></div>{loading ? <div className="upload-history-empty">正在读取 WeKnora 上传记录…</div> : uploads.length === 0 ? <div className="upload-history-empty">暂无文件。上传首份研究材料后会显示在这里。</div> : uploads.map(upload => { const [label, tone] = uploadStatus(upload.parse_status); return <div className="queue" key={upload.id}><span className="file-icon">{upload.file_type.slice(0, 1) || 'F'}</span><div><b>{upload.name}</b><p>{formatUploadSize(upload.size)} · {new Date(upload.created_at).toLocaleString()} {upload.error_message ? `· ${upload.error_message}` : ''}</p></div><Pill tone={tone}>{label}</Pill></div> })}</Card><Card><div className="eyebrow">GOVERNANCE · 01 PENDING</div><h2>观点演变确认</h2><p className="conflict-copy">新报告《ETH 8 月综合研判》与 6 月《ETH 中期展望》在“质押解锁影响”上判断相反。</p><div className="conflict"><span>旧观点</span><b>供给扰动将压制反弹高度</b><span>新观点</span><b>中期结构未破坏，短线受杠杆主导</b></div>{decision ? <div className="decision-done">✓ 已记录裁决：{decision}</div> : <div className="decision-buttons"><button onClick={() => setDecision('更新旧观点')}>更新旧观点</button><button onClick={() => setDecision('两者并存')}>两者并存</button><button onClick={() => setDecision('AI 识别误判')}>驳回提示</button></div>}<p className="fine-print">AI 只负责发现矛盾；最终裁决权属于研究负责人。</p></Card></div>
}

type RiskSymbol = { symbol: string, turnover: number }
type RiskSnapshot = { symbol: string, observed_at: string, mark_price: number, spot_price: number, oi_usd: number, funding_rate: number, cvd_delta_usd: number, basis_pct: number, venue_count: number }
type RiskZone = { side: string, price: number, notional_usd: number, distance_pct: number }
type RiskSignal = { id: string, title: string, detail: string, active: boolean, severity: string }
type RiskEvent = { id: number, symbol: string, level: string, trigger_count: number, signals: RiskSignal[], created_at: string, telegram_status: string }
type RiskRadar = { snapshot: RiskSnapshot, zones: RiskZone[], signals: RiskSignal[], events: RiskEvent[], status: string }
const riskMoney = (value: number) => value >= 1e9 ? `$${(value / 1e9).toFixed(2)}B` : value >= 1e6 ? `$${(value / 1e6).toFixed(2)}M` : value >= 1e3 ? `$${(value / 1e3).toFixed(1)}K` : `$${value.toFixed(0)}`
const riskTone = (level: string) => level === 'CRITICAL' || level === 'HIGH' ? 'red' : level === 'MEDIUM' ? 'amber' : 'muted'

function RiskPage() {
  const [symbols, setSymbols] = useState<RiskSymbol[]>([{ symbol: 'ETH-USDT', turnover: 0 }]); const [symbol, setSymbol] = useState('ETH-USDT'); const [radar, setRadar] = useState<RiskRadar | null>(null); const [events, setEvents] = useState<RiskEvent[]>([]); const [error, setError] = useState(''); const [loading, setLoading] = useState(true)
  useEffect(() => { const controller = new AbortController(); void fetch('/api/v1/risk-radar/symbols', { signal: controller.signal }).then(async response => { const result = await response.json() as { symbols?: RiskSymbol[], error?: string }; if (!response.ok) throw new Error(result.error || '无法读取风险雷达币对目录'); if (result.symbols?.length) setSymbols(result.symbols) }).catch(reason => { if ((reason as Error).name !== 'AbortError') setError((reason as Error).message) }); return () => controller.abort() }, [])
  useEffect(() => { let active = true; const load = async () => { setLoading(true); try { const [snapshotResponse, eventResponse] = await Promise.all([fetch(`/api/v1/risk-radar/snapshot?symbol=${encodeURIComponent(symbol)}`, { cache: 'no-store' }), fetch('/api/v1/risk-radar/events', { cache: 'no-store' })]); const snapshot = await snapshotResponse.json() as RiskRadar & { error?: string }; const eventData = await eventResponse.json() as { events?: RiskEvent[] }; if (!snapshotResponse.ok) throw new Error(snapshot.error || '风险雷达尚未准备完成'); if (active) { setRadar(snapshot); setEvents(eventData.events ?? []); setError('') } } catch (reason) { if (active) { setRadar(null); setError(reason instanceof Error ? reason.message : '风险雷达加载失败') } } finally { if (active) setLoading(false) } }; void load(); const timer = window.setInterval(() => void load(), 30000); return () => { active = false; window.clearInterval(timer) } }, [symbol])
  const snapshot = radar?.snapshot; const activeSignals = radar?.signals.filter(item => item.active).length ?? 0; const latest = radar?.events[0]; const nearest = radar?.zones.slice().sort((left, right) => left.distance_pct - right.distance_pct)[0]
  return <div className="risk-page"><div className="risk-toolbar"><label>监控币对<select value={symbol} onChange={event => setSymbol(event.target.value)}>{symbols.map(item => <option key={item.symbol} value={item.symbol}>{item.symbol}</option>)}</select></label><span>全市场 {symbols.length} 个 Binance / OKX USDT 永续合约 · 30 秒评估</span></div>{error ? <Card className="risk-error"><b>风险雷达暂不可用</b><p>{error}</p><small>需要 PostgreSQL 与公开市场数据完成首个 5 分钟快照；页面不会显示示例数据。</small></Card> : <><div className="risk-banner"><div><div className="eyebrow">{symbol} · OBSERVED LIQUIDATION RISK</div><h1>{latest ? `${latest.level} 风险事件` : activeSignals >= 2 ? '风险信号共振' : '杠杆风险观察'}</h1><p>实际强平密度、OI、Funding、CVD 与基差 · 仅研究提示，不构成交易建议</p></div><div className="risk-level"><span>当前等级</span><b>{latest?.level ?? 'OBSERVE'}</b><em>{snapshot ? `数据快照 ${new Date(snapshot.observed_at).toLocaleTimeString()}` : '等待首个快照'}</em></div></div><div className="metrics">{[['价格', snapshot ? `$${snapshot.mark_price.toLocaleString(undefined, { maximumFractionDigits: 6 })}` : '—', snapshot ? `现货 $${snapshot.spot_price.toLocaleString(undefined, { maximumFractionDigits: 6 })}` : '', 'green'], ['最近观测热区', nearest ? nearest.price.toLocaleString(undefined, { maximumFractionDigits: 6 }) : '—', nearest ? `${nearest.side} · 距现价 ${nearest.distance_pct.toFixed(2)}%` : '尚无足够强平事件', 'amber'], ['未平仓合约', snapshot ? riskMoney(snapshot.oi_usd) : '—', snapshot ? `${snapshot.venue_count} 个交易所数据源` : '', 'red'], ['Funding / 基差', snapshot ? `${(snapshot.funding_rate * 100).toFixed(4)}%` : '—', snapshot ? `基差 ${snapshot.basis_pct.toFixed(3)}% · CVD ${riskMoney(snapshot.cvd_delta_usd)}` : '', 'violet']].map(([label, value, detail, tone]) => <Card key={label}><label>{label}</label><h2>{value}</h2><Pill tone={tone}>{detail || '采集中'}</Pill></Card>)}</div><div className="two-columns"><Card><div className="card-heading"><div><div className="eyebrow">EXPLAINABLE SIGNALS</div><h2>共振触发依据</h2></div><Pill tone={activeSignals >= 2 ? 'red' : 'muted'}>{activeSignals} / 5 维度</Pill></div>{radar?.signals.map(signal => <div className="signal" key={signal.id}><span className={signal.active ? 'signal-on' : ''} /><div><b>{signal.title}</b><p>{signal.detail}</p></div><Pill tone={signal.active ? riskTone(signal.severity === 'high' ? 'HIGH' : 'MEDIUM') : 'muted'}>{signal.active ? '触发' : '观察'}</Pill></div>) ?? <p>{loading ? '正在读取最新风险快照…' : '暂无风险信号。'}</p>}</Card><Card><div className="eyebrow">LATEST EVENT</div><h2>{latest ? `风险提示 #${latest.id}` : '暂无主动风险事件'}</h2><p className="detail-meta">{latest ? `${latest.symbol} · ${new Date(latest.created_at).toLocaleString()}` : '至少两项维度显著变化才生成事件'}</p><div className="detail-section"><label>触发说明</label><p>{latest ? latest.signals.filter(item => item.active).map(item => item.title).join('、') : '当前仅保留观察，不发送 Telegram。'}</p></div><div className="detail-section"><label>Telegram 投递</label><Pill tone={latest?.telegram_status === 'sent' ? 'green' : latest?.telegram_status === 'failed' ? 'red' : 'muted'}>{latest?.telegram_status ?? '未触发'}</Pill></div></Card></div><Card className="risk-ranking"><div className="card-heading"><div><div className="eyebrow">MARKET-WIDE ALERTS</div><h2>全市场最新风险事件</h2></div><Pill tone="violet">{events.length} 条</Pill></div>{events.length ? <table><thead><tr><th>时间</th><th>币对</th><th>等级</th><th>共振</th><th>触发依据</th><th>投递</th></tr></thead><tbody>{events.slice(0, 12).map(event => <tr key={event.id}><td>{new Date(event.created_at).toLocaleString()}</td><td><b>{event.symbol}</b></td><td><Pill tone={riskTone(event.level)}>{event.level}</Pill></td><td>{event.trigger_count} 项</td><td>{event.signals.filter(item => item.active).map(item => item.title).join('、')}</td><td>{event.telegram_status}</td></tr>)}</tbody></table> : <p>暂无满足两项共振条件的全市场风险事件。</p>}</Card></>}</div>
}

function LiquidationMapPage() {
  const url = 'http://10.15.0.6/monitor?days=30'
  return <Card className="liquidation-monitor"><div className="card-heading"><div><div className="eyebrow">LIQUIDATION MAP · 30D</div><h2>清算地图</h2><p>ETH 30 日清算热区研究页面。该页面独立于风险雷达，展示源站的清算结构视图。</p></div><a className="monitor-link" href={url} target="_blank" rel="noreferrer">在新窗口打开 ↗</a></div><div className="monitor-frame"><iframe title="ETH 清算地图（30日）" src={url} loading="lazy" /></div><p className="monitor-fallback">如内嵌页面无法加载，请使用“在新窗口打开”；风险雷达仍会继续使用自身的公开市场监控数据。</p></Card>
}

type LiquidationEvent = { exchange: 'binance' | 'okx', symbol: string, occurredAt: string, side: 'long' | 'short', price: number, quantity: number, notional: number }
type LiquidationCandle = { openTime: string, open: number, high: number, low: number, close: number }
type LiquidationSymbol = { symbol: string, turnover: number }
type LiquidationExchangeStatus = { connected: boolean, lastEvent?: string, lastDirectEvent?: string, lastFallbackEvent?: string, lastRawMessage?: string, lastParseError?: string, error?: string }
type LiquidationChart = { symbol: string, interval: CandleInterval, collectionStartedAt?: string, candles: LiquidationCandle[], events: LiquidationEvent[], status: { exchanges: Record<string, LiquidationExchangeStatus>, fallback?: { enabled: boolean, lastSuccess?: string, error?: string } } }
type ZoomWindow = { start: number, end: number }
type LiquidationFilterKind = 'notional' | 'quantity'
type CandleInterval = '5m' | '15m' | '1h'
const chartRanges = ['1h', '4h', '8h', '12h', '24h', '7d'] as const
const candleIntervals: CandleInterval[] = ['5m', '15m', '1h']
const formatMoney = (value: number) => value >= 1_000_000 ? `$${(value / 1_000_000).toFixed(2)}M` : value >= 1_000 ? `$${(value / 1_000).toFixed(1)}K` : `$${value.toFixed(0)}`
const formatQuantity = (value: number) => value >= 1_000 ? `${(value / 1_000).toFixed(1)}K` : value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1) : value >= 1 ? value.toFixed(2) : value.toPrecision(2)

function LiquidationBubblePage({ theme }: { theme: Theme }) {
  const element = useRef<HTMLDivElement>(null); const instance = useRef<echarts.ECharts | null>(null); const zoomWindow = useRef<ZoomWindow | null>(null); const viewportKey = useRef('')
  const [symbols, setSymbols] = useState<LiquidationSymbol[]>([{ symbol: 'ETH-USDT', turnover: 0 }]); const [symbol, setSymbol] = useState('ETH-USDT'); const [selectedRange, setSelectedRange] = useState<(typeof chartRanges)[number]>('4h'); const [candleInterval, setCandleInterval] = useState<CandleInterval>('5m'); const [venue, setVenue] = useState('all'); const [filterKind, setFilterKind] = useState<LiquidationFilterKind>('notional'); const [filterMinimum, setFilterMinimum] = useState('10000'); const [showBubbleQuantity, setShowBubbleQuantity] = useState(true); const [data, setData] = useState<LiquidationChart | null>(null); const [error, setError] = useState(''); const [loading, setLoading] = useState(true)
  const minimum = Number(filterMinimum); const validMinimum = Number.isFinite(minimum) && minimum > 0; const baseAsset = symbol.split('-')[0] || symbol; const filterUnit = filterKind === 'notional' ? 'USDT' : baseAsset; const filterLabel = filterKind === 'notional' ? `金额 ≥ ${formatMoney(minimum)}` : `数量 ≥ ${minimum || 0} ${baseAsset}`
  const eventMatchesFilter = (item: LiquidationEvent) => validMinimum && (filterKind === 'notional' ? item.notional >= minimum : item.price > 0 && item.notional / item.price >= minimum)
  const changeFilterKind = (next: LiquidationFilterKind) => { setFilterKind(next); setFilterMinimum(next === 'notional' ? '10000' : '1') }
  useEffect(() => { const controller = new AbortController(); void fetch('/api/v1/liquidations/symbols', { signal: controller.signal }).then(async response => { if (!response.ok) throw new Error('无法读取币对目录'); const result = await response.json() as { symbols?: LiquidationSymbol[] }; if (result.symbols?.length) setSymbols(result.symbols) }).catch(fetchError => { if ((fetchError as Error).name !== 'AbortError') setError((fetchError as Error).message) }); return () => controller.abort() }, [])
  useEffect(() => {
    const nextViewportKey = `${symbol}:${selectedRange}:${candleInterval}:${venue}`
    if (viewportKey.current !== nextViewportKey) { zoomWindow.current = null; viewportKey.current = nextViewportKey }
    if (!validMinimum) { setLoading(false); setError('过滤阈值必须为大于 0 的数字'); return }
    const controller = new AbortController(); setLoading(true); setError('')
    const query = new URLSearchParams({ symbol, range: selectedRange, interval: candleInterval, exchanges: venue, filter: filterKind, min: String(minimum) })
    void fetch(`/api/v1/liquidations/chart?${query}`, { signal: controller.signal }).then(async response => { const result = await response.json() as LiquidationChart & { error?: string }; if (!response.ok) throw new Error(result.error || '无法读取爆仓数据'); setData(result) }).catch(fetchError => { if ((fetchError as Error).name !== 'AbortError') { setData(null); setError((fetchError as Error).message) } }).finally(() => setLoading(false))
    return () => controller.abort()
  }, [symbol, selectedRange, candleInterval, venue, filterKind, filterMinimum, minimum, validMinimum])
  useEffect(() => { if (!element.current) return; const chart = echarts.init(element.current); const recordZoom = (event: unknown) => { const payload = event as { start?: number, end?: number, batch?: Array<{ start?: number, end?: number }> }; const window = payload.batch?.find(item => typeof item.start === 'number' && typeof item.end === 'number') ?? payload; if (typeof window.start === 'number' && typeof window.end === 'number') zoomWindow.current = { start: window.start, end: window.end } }; instance.current = chart; chart.on('datazoom', recordZoom); const observer = new ResizeObserver(() => chart.resize()); observer.observe(element.current); return () => { observer.disconnect(); chart.off('datazoom', recordZoom); chart.dispose(); instance.current = null } }, [])
  useEffect(() => {
    const chart = instance.current; if (!chart) return
    const candles = data?.candles ?? []; const events = data?.events ?? []
    const sortedCandles = [...candles].sort((left, right) => Date.parse(left.openTime) - Date.parse(right.openTime))
    const priceSpan = sortedCandles.length ? Math.max(...sortedCandles.map(item => item.high)) - Math.min(...sortedCandles.map(item => item.low)) : 0
    const bubblePadding = Math.max(priceSpan * .012, .01)
    const candleAt = (occurredAt: string) => {
      const timestamp = Date.parse(occurredAt); let selected = sortedCandles[0]
      for (const candle of sortedCandles) { if (Date.parse(candle.openTime) > timestamp) break; selected = candle }
      return selected
    }
    const minNotional = events.length ? Math.min(...events.map(item => item.notional)) : 0; const maxNotional = events.length ? Math.max(...events.map(item => item.notional)) : 0
    const bubbleSize = (notional: number) => maxNotional <= minNotional ? 24 : 12 + 46 * Math.sqrt(Math.max(0, (notional - minNotional) / (maxNotional - minNotional)))
    const zoom = zoomWindow.current; const zoomState = zoom ? { start: zoom.start, end: zoom.end } : {}
    const bubbles = (side: 'long' | 'short', color: string, name: string) => {
      const groups = new Map<string, Array<{ item: LiquidationEvent, candle?: LiquidationCandle }>>()
      for (const item of events.filter(event => event.side === side)) {
        const candle = candleAt(item.occurredAt)
        const key = candle?.openTime ?? item.occurredAt
        const group = groups.get(key) ?? []
        group.push({ item, candle })
        groups.set(key, group)
      }
      const data = Array.from(groups.values()).flatMap(group => {
        group.sort((left, right) => Date.parse(left.item.occurredAt) - Date.parse(right.item.occurredAt) || right.item.notional - left.item.notional)
        let distance = 0
        return group.map(({ item, candle }) => {
          const radiusInPrice = priceSpan * Math.max(20, bubbleSize(item.notional)) / 720
          distance += radiusInPrice + bubblePadding
          const anchor = candle ? (side === 'long' ? candle.low : candle.high) : item.price
          const plotPrice = side === 'long' ? anchor - distance : anchor + distance
          distance += radiusInPrice
          return [item.occurredAt, plotPrice, item.notional, item.exchange, item.quantity, item.price]
        })
      })
      return {
      name, type: 'scatter' as const, data,
      symbolOffset: [0, side === 'long' ? 12 : -12], symbolSize: (value: unknown[]) => bubbleSize(Number(value[2])), label: { show: showBubbleQuantity, position: 'inside', color: chartTheme.bubbleLabel, fontSize: 9, fontWeight: 700, formatter: (params: { value?: unknown[] }) => formatQuantity(Number(params.value?.[4])) }, itemStyle: { color, opacity: .72 }, z: 6
      }
    }
    const candleLabel = `OKX ${data?.interval ?? candleInterval} K线`
    const chartTheme = theme === 'light'
      ? { muted: '#52657c', grid: '#d7e0ec', tooltip: '#ffffff', tooltipBorder: '#b6c4d6', text: '#18243a', sliderBorder: '#b8c6d8', sliderFill: '#725cbb40', bubbleLabel: '#1f3149' }
      : { muted: '#9eacc1', grid: '#243044', tooltip: '#101925', tooltipBorder: '#3a4b65', text: '#e9eef7', sliderBorder: '#314058', sliderFill: '#725cbb55', bubbleLabel: '#eef5ff' }
    chart.setOption({
      animation: false, backgroundColor: 'transparent', grid: { left: 58, right: 28, top: 44, bottom: 68 },
      legend: { top: 8, textStyle: { color: chartTheme.muted, fontSize: 11 }, data: [candleLabel, '多头爆仓', '空头爆仓'] },
      tooltip: { trigger: 'axis', axisPointer: { type: 'cross' }, backgroundColor: chartTheme.tooltip, borderColor: chartTheme.tooltipBorder, textStyle: { color: chartTheme.text }, formatter: (raw: unknown) => {
        const items = raw as Array<{ seriesName: string, value: unknown[] }>
        return items.filter(item => item.seriesName !== candleLabel).map(item => `<b>${item.seriesName}</b><br/>时间：${new Date(String(item.value[0])).toLocaleString()}<br/>价格：${Number(item.value[5] ?? item.value[1]).toLocaleString()}<br/>名义价值：${formatMoney(Number(item.value[2]))}<br/>交易所：${item.value[3]}<br/>数量：${Number(item.value[4]).toLocaleString()}`).join('<hr/>')
      } },
      xAxis: { type: 'time', axisLine: { lineStyle: { color: chartTheme.muted } }, axisLabel: { color: chartTheme.muted }, splitLine: { show: false } },
      yAxis: { scale: true, axisLine: { lineStyle: { color: chartTheme.muted } }, axisLabel: { color: chartTheme.muted }, splitLine: { lineStyle: { color: chartTheme.grid } } },
      dataZoom: [{ type: 'inside', xAxisIndex: 0, zoomOnMouseWheel: true, moveOnMouseMove: true, moveOnMouseWheel: false, preventDefaultMouseMove: true, ...zoomState }, { type: 'slider', xAxisIndex: 0, height: 18, bottom: 18, borderColor: chartTheme.sliderBorder, fillerColor: chartTheme.sliderFill, textStyle: { color: chartTheme.muted }, ...zoomState }],
      series: [{ name: candleLabel, type: 'candlestick', data: candles.map(item => [item.openTime, item.open, item.close, item.low, item.high]), itemStyle: { color: '#57c995', color0: '#ec6e83', borderColor: '#57c995', borderColor0: '#ec6e83' } }, bubbles('long', '#f06e84', '多头爆仓'), bubbles('short', '#55d7a1', '空头爆仓')],
    }, true)
    if (candles.length === 0) chart.clear()
  }, [data, theme, showBubbleQuantity, candleInterval])
  useEffect(() => { if (!validMinimum) return; const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'; const query = new URLSearchParams({ symbol, exchanges: venue, filter: filterKind, min: String(minimum) }); const socket = new WebSocket(`${protocol}://${window.location.host}/api/v1/liquidations/stream?${query}`); socket.onmessage = message => { const result = JSON.parse(message.data) as { type?: string, event?: LiquidationEvent }; if (result.type === 'liquidation' && result.event && eventMatchesFilter(result.event)) setData(current => current && current.symbol === result.event?.symbol ? { ...current, events: [...current.events, result.event] } : current) }; return () => socket.close() }, [symbol, venue, filterKind, filterMinimum, minimum, validMinimum])
  const started = data?.collectionStartedAt ? new Date(data.collectionStartedAt).toLocaleString() : '尚未收到爆仓事件'; const status = data?.status; const directTime = (exchange: 'binance' | 'okx') => status?.exchanges[exchange]?.lastDirectEvent ? new Date(status.exchanges[exchange].lastDirectEvent).toLocaleTimeString() : '未收到'; const fallbackTime = status?.fallback?.lastSuccess ? new Date(status.fallback.lastSuccess).toLocaleTimeString() : '同步中'; const directTitle = (exchange: 'binance' | 'okx') => { const item = status?.exchanges[exchange]; return [item?.lastRawMessage ? `最近原始帧：${new Date(item.lastRawMessage).toLocaleString()}` : '', item?.lastParseError ? `解析错误：${item.lastParseError}` : '', item?.error ? `连接错误：${item.error}` : ''].filter(Boolean).join('\n') }
  return <div className="liquidation-page"><div className="liquidation-top"><div><div className="eyebrow">PUBLIC LIQUIDATION FLOW · BINANCE / OKX</div><h2>爆仓气泡图</h2><p>OKX USDT 永续 K 线叠加公开爆仓事件。数据由交易所直连与备源持续补漏。</p></div><div className="liquidation-status"><span className={status?.exchanges.binance?.connected ? 'online' : ''} title={directTitle('binance')}>Binance 直连 · {directTime('binance')}</span><span className={status?.exchanges.okx?.connected ? 'online' : ''} title={directTitle('okx')}>OKX 直连 · {directTime('okx')}</span><span className={status?.fallback?.lastSuccess ? 'online' : ''} title={status?.fallback?.error}>备源同步 · {fallbackTime}</span></div></div><Card className="liquidation-chart-card"><div className="liquidation-controls"><label>币对<select value={symbol} onChange={event => setSymbol(event.target.value)}>{symbols.map(item => <option key={item.symbol}>{item.symbol}</option>)}</select></label><div className="interval-buttons"><span>K线周期</span>{candleIntervals.map(item => <button key={item} className={candleInterval === item ? 'active' : ''} onClick={() => setCandleInterval(item)}>{item}</button>)}</div><div className="range-buttons">{chartRanges.map(item => <button key={item} className={selectedRange === item ? 'active' : ''} onClick={() => setSelectedRange(item)}>{item}</button>)}</div><div className="venue-buttons">{[['all', '全部'], ['binance', 'Binance'], ['okx', 'OKX']].map(([id, label]) => <button key={id} className={venue === id ? 'active' : ''} onClick={() => setVenue(id)}>{label}</button>)}</div><div className="filter-controls"><span>过滤</span><div className="filter-buttons"><button className={filterKind === 'notional' ? 'active' : ''} onClick={() => changeFilterKind('notional')}>金额</button><button className={filterKind === 'quantity' ? 'active' : ''} onClick={() => changeFilterKind('quantity')}>数量</button></div><label>≥ <input aria-label="爆仓过滤阈值" type="number" min="0" step="any" value={filterMinimum} onChange={event => setFilterMinimum(event.target.value)} /> <b>{filterUnit}</b></label></div><label className="bubble-quantity-toggle"><input type="checkbox" checked={showBubbleQuantity} onChange={event => setShowBubbleQuantity(event.target.checked)} /> 显示数量</label><span className="chart-gesture-hint">滚轮缩放 · 左键拖动横移</span></div><div className="collection-note">爆仓数据采集起始：{started} · 当前展示 {data?.events.length ?? 0} 个原始事件 · K线周期：{data?.interval ?? candleInterval} · 筛选：{filterLabel}</div><div className="liquidation-chart-wrap"><div className="liquidation-chart" ref={element} />{error ? <div className="liquidation-empty error">{error}<small>请填写有效阈值，或检查爆仓数据服务是否可用。</small></div> : loading ? <div className="liquidation-empty">正在加载 {symbol} 图表数据…</div> : data && data.candles.length === 0 ? <div className="liquidation-empty">K 线正在从 OKX 公开流累积。连接建立后会自动刷新。</div> : null}</div></Card></div>
}

type WatchlistScenario = { label: 'Bull' | 'Base' | 'Bear', content: string }
type WatchlistNews = { title: string, summary: string, source: string, published_at: string, url?: string }
type WatchlistBrief = { generated_at: string, crypto: WatchlistScenario[], us_equities: WatchlistScenario[], news: WatchlistNews[], cached: boolean, error?: string }

const watchlistTone = (label: WatchlistScenario['label']) => label === 'Bull' ? 'green' : label === 'Bear' ? 'red' : 'muted'
const watchlistTime = (value: string) => { const parsed = new Date(value); return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString() }

function WatchlistPage() {
  const [brief, setBrief] = useState<WatchlistBrief | null>(null); const [loading, setLoading] = useState(true); const [refreshing, setRefreshing] = useState(false); const [error, setError] = useState('')
  const loadBrief = useCallback(async (force = false) => {
    force ? setRefreshing(true) : setLoading(true)
    try {
      const response = await fetch(`/api/v1/watchlist/brief${force ? '?refresh=1' : ''}`)
      const result = await response.json().catch(() => null) as WatchlistBrief | null
      if (!response.ok || !result?.generated_at) throw new Error(result?.error || '无法生成实时市场简报')
      setBrief(result); setError('')
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法生成实时市场简报')
    } finally {
      setLoading(false); setRefreshing(false)
    }
  }, [])
  useEffect(() => { void loadBrief() }, [loadBrief])
  const scenarioCard = (title: string, eyebrow: string, scenarios: WatchlistScenario[]) => <Card className="scenario-card"><div className="scenario-card-head"><div><div className="eyebrow">{eyebrow}</div><h2>{title}</h2></div><span>实时情景</span></div><div className="scenario-list">{scenarios.map(scenario => <div className="scenario-row" key={scenario.label}><Pill tone={watchlistTone(scenario.label)}>{scenario.label}</Pill><p>{scenario.content}</p></div>)}</div></Card>
  return <div className="watchlist-brief"><div className="watchlist-toolbar"><div><div className="eyebrow">LIVE MARKET BRIEF</div><span>{brief ? `${brief.cached ? '15 分钟缓存快照' : '刚刚完成实时检索'} · 更新于 ${watchlistTime(brief.generated_at)}` : '通过 WeKnora 实时检索外部市场与新闻'}</span></div><button className="primary" type="button" disabled={loading || refreshing} onClick={() => void loadBrief(true)}>{refreshing ? '正在刷新…' : '↻ 刷新信息'}</button></div>{error && <div className="watchlist-error" role="alert">{error}{brief && <small>下方保留上一次成功获取的简报。</small>}</div>}{loading && !brief ? <Card className="watchlist-loading"><span className="answer-loading-indicator" /><div><b>正在检索 Crypto、美股与重大新闻…</b><p>仅使用实时外部来源，不调用内部知识库。</p></div></Card> : brief ? <><div className="scenario-grid">{scenarioCard('今日 Crypto 三情景', 'CRYPTO OUTLOOK', brief.crypto)}{scenarioCard('今晚美股三情景', 'US EQUITIES OUTLOOK', brief.us_equities)}</div><section className="major-news"><div className="major-news-head"><div><h2>全球最新重大新闻雷达</h2><span>实时检索，不以行情总结替代新闻</span></div><span className="news-count">{brief.news.length} 条高影响事件</span></div><div className="major-news-grid">{brief.news.map((item, index) => <article className="major-news-item" key={`${item.title}-${index}`}><span className="news-index">{index + 1}</span><div><div className="news-title-line"><h3>{item.title}</h3>{item.url && <a href={item.url} target="_blank" rel="noreferrer">原文 ↗</a>}</div><p>{item.summary}</p><small>{item.source} · {item.published_at}</small></div></article>)}</div><p className="watchlist-disclaimer">研究用途 · 新闻驱动情景不构成投资建议</p></section></> : null}</div>
}

function CockpitPage() { return <><div className="cockpit-note">驾驶舱为聚合层 · 当前展示来自研究记忆、风险雷达与关注池的真实页面状态</div><div className="market-strip">{[['S&P 500','7,728','-0.3%'],['Nasdaq','26,445','-0.6%'],['VIX','15.5','平'],['BTC','$64.7K','↑'],['ETH','$1,904','↑']].map(x => <div key={x[0]}><span>{x[0]}</span><b>{x[1]}</b><em>{x[2]}</em></div>)}</div><div className="cockpit-grid"><Card><div className="eyebrow">DECISION RADAR</div><h2>距上次查看以来</h2>{['ETH · 风险升高 — 清算区上移，OI 持续增加','AI 光通信 · 逻辑强化 — 客户 Capex 预期上调','NVDA · 定价观察 — 预期差正在收窄'].map(x => <div className="radar-item" key={x}>● {x}</div>)}</Card><Card><div className="eyebrow">RESEARCH FEED</div><h2>研究成果流</h2>{reports.slice(0, 3).map(r => <div className="feed" key={r.id}><b>{r.title}</b><span>{r.date} · {r.type}</span></div>)}</Card></div></> }

function SettingsPage({ page }: { page: Page }) { const isSource = page === 'sources'; const isRules = page === 'rules'; return <Card><div className="eyebrow">ADMIN ONLY</div><h2>{pageMeta[page][0]}</h2><p className="settings-intro">所有配置修改均写入机构审计日志，并由相应权限范围控制。</p>{isSource && <><Setting title="WeKnora 机构研究知识库" detail="已连接 · 1,284 个文档 · 最近同步 8 分钟前" enabled /><Setting title="研究邮箱附件入库" detail="首期关闭；保留后续授权接入位" /><Setting title="网盘目录增量同步" detail="首期关闭；仅允许白名单目录" /></>}{isRules && <><Setting title="多维共振主动提示" detail="≥ 2 个显著风险维度同时触发" enabled /><Setting title="最高优先级" detail="4 个风险维度同时异常" enabled /><Setting title="自动化交易执行" detail="硬性禁用：风险模块不得连接下单 API" /></>}{page === 'audit' && <table><thead><tr><th>成员</th><th>角色</th><th>可访问范围</th><th>最近活动</th></tr></thead><tbody><tr><td><b>林然</b></td><td>研究负责人</td><td>全部研究与裁决</td><td>14:35 · 确认风险提示</td></tr><tr><td><b>陈可</b></td><td>研究员</td><td>加密、宏观</td><td>13:12 · 上传报告</td></tr><tr><td><b>Alex</b></td><td>观察者</td><td>已发布研究</td><td>昨日 17:40 · 阅读</td></tr></tbody></table>}</Card> }
function Setting({ title, detail, enabled = false }: {title:string, detail:string, enabled?:boolean}) { const [on, setOn] = useState(enabled); return <div className="setting"><div><b>{title}</b><p>{detail}</p></div><button onClick={() => setOn(!on)} className={`toggle ${on ? 'on' : ''}`}><i /></button></div> }

function App() {
  const [page, setPage] = useState<Page>('ask'); const [collapsed, setCollapsed] = useState(false); const [theme, setTheme] = useState<Theme>(storedTheme)
  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem(themeStorageKey, theme) }, [theme])
  const render = () => ({ask:<AskPage />, memory:<MemoryPage />, studio:<StudioPage />, risk:<RiskPage />, liquidationMap:<LiquidationMapPage />, liquidation:<LiquidationBubblePage theme={theme} />, watchlist:<WatchlistPage />, cockpit:<CockpitPage />, sources:<SettingsPage page="sources" />, rules:<SettingsPage page="rules" />, audit:<SettingsPage page="audit" />}[page])
  const [title, subtitle] = pageMeta[page]
  return <div className={`app-shell ${collapsed ? 'collapsed' : ''}`} data-theme={theme}><aside><div className="brand"><span>R</span><div><b>RESEARCH OS</b><small>机构研究工作台</small></div><button onClick={() => setCollapsed(!collapsed)}>☰</button></div><nav>{navGroups.map(group => <div className="nav-group" key={group.label}><label>{group.label}</label>{group.items.map(([id, icon, text]) => <button key={id} onClick={() => setPage(id)} className={page === id ? 'active' : ''}><i>{icon}</i><span>{text}</span>{id === 'risk' && <em>2</em>}</button>)}</div>)}</nav><div className="user"><div className="avatar">LR</div><div><b>林然</b><small>研究负责人</small></div><span>⌄</span></div></aside><main><header><div><div className="breadcrumb">RESEARCH OS / {title}</div><h1>{title}</h1><p>{subtitle}</p></div><div className="header-actions"><div className="theme-switcher" role="group" aria-label="界面主题"><button type="button" aria-pressed={theme === 'light'} className={theme === 'light' ? 'active' : ''} onClick={() => setTheme('light')}>☀ 浅色</button><button type="button" aria-pressed={theme === 'dark'} className={theme === 'dark' ? 'active' : ''} onClick={() => setTheme('dark')}>☾ 深色</button></div><button className="notice">◉<span>2</span></button><span className="status"><i /> HYGR 智能问答已连接</span></div></header><div className="page-content">{render()}</div><BuildFooter /></main></div>
}

export default App
