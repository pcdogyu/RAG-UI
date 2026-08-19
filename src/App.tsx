import { useMemo, useState } from 'react'
import { navGroups, reports, type Report } from './data'

type Page = typeof navGroups[number]['items'][number][0]
const pageMeta: Record<Page, [string, string]> = {
  ask: ['智能研究问答', '带着整个团队的研究记忆，与当下市场对话'],
  memory: ['机构研究记忆', '将报告、证据与投资判断沉淀为可调用的机构资产'],
  studio: ['研究工作区', '上传、校验与裁决，让 AI 辅助而不替代研究责任'],
  risk: ['ETH 风险雷达', '可解释的多维风险共振监测 · 不连接自动交易'],
  watchlist: ['重点关注池', '集中查看标的、主题与持仓的研究状态'],
  cockpit: ['研究驾驶舱', '面向团队的研究状态聚合 · 内容成熟后逐步启用'],
  sources: ['知识库与数据接入', '管理 WeKnora 知识库与已授权的数据同步源'],
  rules: ['风险规则配置', '调整阈值与提醒范围，所有修改均保留审计记录'],
  audit: ['成员权限与审计', '研究数据的访问边界与关键操作留痕'],
}

const Pill = ({ children, tone = 'neutral' }: { children: React.ReactNode, tone?: string }) => <span className={`pill ${tone}`}>{children}</span>
const Card = ({ children, className = '' }: { children: React.ReactNode, className?: string }) => <section className={`card ${className}`}>{children}</section>

function AskPage() {
  const [scope, setScope] = useState('内部 + 实时')
  const [question, setQuestion] = useState('ETH 过去一周的核心逻辑变化是什么？')
  const [asked, setAsked] = useState(true)
  const [answer, setAnswer] = useState('本轮 ETH 回调以杠杆去化为主，现货贡献有限；中期研究框架暂未改变。')
  const [citations, setCitations] = useState<{ id: string, title: string, url?: string }[]>([])
  const [answerSource, setAnswerSource] = useState('演示结果')
  const submit = async () => {
    setAsked(true)
    try {
      const response = await fetch('/api/v1/research/ask', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ question, scope }) })
      if (!response.ok) throw new Error('research request failed')
      const result = await response.json() as { source?: string, answer?: { conclusion?: string, citations?: { id: string, title: string, url?: string }[] } }
      if (result.answer?.conclusion) { setAnswer(result.answer.conclusion); setCitations(result.answer.citations ?? []); setAnswerSource(result.source === 'weknora-agent' ? 'WeKnora · 机构研究工作台' : '研究服务') }
    } catch { setAnswer('当前无法连接 WeKnora 机构研究工作台。请确认 Go 服务已启动，且已配置 WeKnora 凭据。'); setCitations([]); setAnswerSource('连接失败') }
  }
  return <div className="ask-layout">
    <Card className="ask-hero">
      <div className="eyebrow">ASK RESEARCH <span>WEKNORA RETRIEVAL READY</span></div>
      <h2>今天，想验证什么判断？</h2>
      <p>答案会区分历史观点与当前信息，并保留每条证据的时间锚点。</p>
      <div className="scope-row">{['仅内部', '内部 + 实时', '仅原始来源'].map(item => <button key={item} onClick={() => setScope(item)} className={`scope ${scope === item ? 'active' : ''}`}>{item}</button>)}</div>
      <div className="ask-box"><input value={question} onChange={e => setQuestion(e.target.value)} onKeyDown={e => e.key === 'Enter' && void submit()} /><button onClick={() => void submit()}>发起研究 →</button></div>
      <div className="quick-row">{['问财报', '问事件', '问标的', '问持仓', '问历史'].map(x => <button key={x} onClick={() => { setQuestion(`${x}：请给出当前最重要的研究结论`); setAsked(true) }}>{x}</button>)}</div>
    </Card>
    {asked && <Card className="answer-card">
      <div className="answer-top"><div><Pill tone="violet">{scope}</Pill><span className="temporal">{answerSource}</span></div><button className="ghost">生成快评 ↗</button></div>
      <div className="question-label">{question}</div>
      <div className="answer-grid">
        <AnswerBlock title="结论" text={answer} />
        <AnswerBlock title="证据" text="OI 在 2 小时内下降 9%，现货净流出占比仅 18%；下方清算密集区距现价约 6%。" />
        <AnswerBlock title="对逻辑的影响" text="短期中性偏空，杠杆风险判断强化；中期配置逻辑仍需以 ETF 资金流和宏观流动性验证。" />
        <AnswerBlock title="下一验证项" text="关注未来 4 小时 Funding 是否转负，以及现货是否出现连续净流出。" />
      </div>
      <div className="citations"><span>检索引用</span>{citations.length ? citations.map((citation, i) => citation.url ? <a key={citation.id || citation.title} href={citation.url} target="_blank" rel="noreferrer">[{i + 1}] {citation.title}</a> : <span className="citation-chip" key={citation.id || citation.title}>[{i + 1}] {citation.title}</span>) : <span className="no-citation">本次回答未返回可展示的引用。</span>}</div>
    </Card>}
  </div>
}

function AnswerBlock({ title, text }: { title: string, text: string }) { return <div className="answer-block"><h4>{title}</h4><p>{text}</p></div> }

function MemoryPage() {
  const [selected, setSelected] = useState<Report>(reports[0]); const [search, setSearch] = useState(''); const [status, setStatus] = useState('全部状态')
  const visible = useMemo(() => reports.filter(r => `${r.title}${r.asset}${r.category}`.toLowerCase().includes(search.toLowerCase()) && (status === '全部状态' || r.status === status)), [search, status])
  return <div className="memory-layout"><div><Card className="filters"><div className="search">⌕ <input placeholder="检索报告、标的、主题…" value={search} onChange={e => setSearch(e.target.value)} /></div><select value={status} onChange={e => setStatus(e.target.value)}><option>全部状态</option><option>当前有效</option><option>已更新</option><option>历史归档</option></select><button className="filter-button">加密 / 链上 / 资金⌄</button></Card><div className="report-count">已收录 <b>{reports.length}</b> 份研究报告 <span>由 WeKnora 知识库索引</span></div><div className="report-list">{visible.map(r => <button className={`report-row ${selected.id === r.id ? 'selected' : ''}`} onClick={() => setSelected(r)} key={r.id}><div><Pill tone={r.status === '当前有效' ? 'green' : 'muted'}>{r.status}</Pill><h3>{r.title}</h3><p>{r.category} · {r.type}</p></div><time>{r.date}</time></button>)}</div></div><ReportDetail report={selected} /></div>
}

function ReportDetail({ report }: { report: Report }) { return <Card className="report-detail"><div className="detail-top"><Pill tone={report.status === '当前有效' ? 'green' : 'amber'}>{report.status}</Pill><button className="ghost">打开原文 ↗</button></div><h2>{report.title}</h2><p className="detail-meta">{report.asset} · 数据截止 {report.date} · 版本 v2.3</p><div className="detail-section"><label>AI 结构化摘要</label><p>{report.summary}</p></div><div className="detail-section"><label>核心投资命题</label><p>{report.thesis}</p></div><div className="detail-section"><label>失效条件</label><p>{report.invalidation}</p></div><div className="detail-section"><label>证据与时间锚点</label>{report.evidence.map(x => <div className="evidence" key={x}><span>◆</span>{x}</div>)}</div><div className="version-line"><b>版本脉络</b><span>06-18 ETH 中期展望</span><i /> <strong>08-10 当前报告</strong></div></Card> }

function StudioPage() { const [decision, setDecision] = useState<string | null>(null); return <div className="two-columns"><Card><div className="card-heading"><div><div className="eyebrow">INGESTION QUEUE</div><h2>待处理研究材料</h2></div><button className="primary">+ 上传报告</button></div><div className="upload-drop">⌁<b>拖放或选择 PDF / Word / PPT</b><span>文件先进入 WeKnora 知识库，结构化解析在后台进行</span></div>{['半导体月度跟踪 · 08-12.pdf', '美国 CPI 事件复盘.docx'].map((x, i) => <div className="queue" key={x}><span className="file-icon">{i ? 'W' : 'P'}</span><div><b>{x}</b><p>{i ? '等待分类' : 'AI 正在抽取研究对象与标签'}</p></div><Pill tone={i ? 'amber' : 'violet'}>{i ? '需确认' : '处理中'}</Pill></div>)}</Card><Card><div className="eyebrow">GOVERNANCE · 01 PENDING</div><h2>观点演变确认</h2><p className="conflict-copy">新报告《ETH 8 月综合研判》与 6 月《ETH 中期展望》在“质押解锁影响”上判断相反。</p><div className="conflict"><span>旧观点</span><b>供给扰动将压制反弹高度</b><span>新观点</span><b>中期结构未破坏，短线受杠杆主导</b></div>{decision ? <div className="decision-done">✓ 已记录裁决：{decision}</div> : <div className="decision-buttons"><button onClick={() => setDecision('更新旧观点')}>更新旧观点</button><button onClick={() => setDecision('两者并存')}>两者并存</button><button onClick={() => setDecision('AI 识别误判')}>驳回提示</button></div>}<p className="fine-print">AI 只负责发现矛盾；最终裁决权属于研究负责人。</p></Card></div> }

function RiskPage() { return <><div className="risk-banner"><div><div className="eyebrow">ETH · RISK MONITOR</div><h1>杠杆风险升级</h1><p>两项以上信号共振触发 · 仅研究提示，不构成交易建议</p></div><div className="risk-level"><span>当前等级</span><b>HIGH</b><em>↑ 较昨日升高</em></div></div><div className="metrics">{[['价格', '$1,904', '+2.4%', 'green'], ['下方清算密集区', '1823.5', '距现价 4.2%', 'amber'], ['未平仓合约', '$4.39B', '24h +11.8%', 'red'], ['Funding', '+0.0002%', '8h', 'violet']].map(([a,b,c,t]) => <Card key={a}><label>{a}</label><h2>{b}</h2><Pill tone={t}>{c}</Pill></Card>)}</div><div className="two-columns"><Card><div className="card-heading"><div><div className="eyebrow">EXPLAINABLE SIGNALS</div><h2>共振触发依据</h2></div><Pill tone="red">2 / 4 维度</Pill></div>{[['清算结构迁移', '下方清算密集区向现价上移', '触发'], ['OI / Funding 异常', 'OI 持续抬升，Funding 维持正值', '触发'], ['价格接近清算区', '尚未进入近端阈值', '观察'], ['现货 / 衍生品背离', '现货未跟涨，需继续跟踪', '观察']].map(([a,b,c]) => <div className="signal" key={a}><span className={c === '触发' ? 'signal-on' : ''} /> <div><b>{a}</b><p>{b}</p></div><Pill tone={c === '触发' ? 'red' : 'muted'}>{c}</Pill></div>)}</Card><Card><div className="eyebrow">LATEST EVENT</div><h2>风险提示 #ETH-0812-04</h2><p className="detail-meta">触发于 14:32 · 数据快照 14:30</p><div className="detail-section"><label>影响说明</label><p>杠杆堆积与下方清算区距离缩短，若现货承接不足，短时波动可能放大。</p></div><div className="detail-section"><label>处理状态</label><Pill tone="amber">待研究员确认</Pill></div><button className="primary wide">标记为已阅</button></Card></div></> }

function WatchlistPage() { return <Card><div className="card-heading"><div><div className="eyebrow">WATCHLIST</div><h2>重点关注池</h2></div><button className="primary">+ 添加关注</button></div><table><thead><tr><th>对象</th><th>当前研究状态</th><th>最新研究</th><th>风险状态</th><th>下一验证项</th></tr></thead><tbody>{[['ETH','框架有效','ETH 8 月综合研判','风险升高','Funding 是否转负'],['NVDA','预期差收窄','财报前预期差跟踪','定价观察','Capex 指引'],['AI 光通信','逻辑强化','产业链更新','正常','订单交付节奏']].map((x, i) => <tr key={x[0]}><td><b>{x[0]}</b><small>{i === 0 ? '加密资产' : i === 1 ? '美股' : '产业主题'}</small></td><td><Pill tone="green">{x[1]}</Pill></td><td>{x[2]}</td><td><Pill tone={i === 0 ? 'red' : 'amber'}>{x[3]}</Pill></td><td>{x[4]}</td></tr>)}</tbody></table></Card> }

function CockpitPage() { return <><div className="cockpit-note">驾驶舱为聚合层 · 当前展示来自研究记忆、风险雷达与关注池的真实页面状态</div><div className="market-strip">{[['S&P 500','7,728','-0.3%'],['Nasdaq','26,445','-0.6%'],['VIX','15.5','平'],['BTC','$64.7K','↑'],['ETH','$1,904','↑']].map(x => <div key={x[0]}><span>{x[0]}</span><b>{x[1]}</b><em>{x[2]}</em></div>)}</div><div className="cockpit-grid"><Card><div className="eyebrow">DECISION RADAR</div><h2>距上次查看以来</h2>{['ETH · 风险升高 — 清算区上移，OI 持续增加','AI 光通信 · 逻辑强化 — 客户 Capex 预期上调','NVDA · 定价观察 — 预期差正在收窄'].map(x => <div className="radar-item" key={x}>● {x}</div>)}</Card><Card><div className="eyebrow">RESEARCH FEED</div><h2>研究成果流</h2>{reports.slice(0, 3).map(r => <div className="feed" key={r.id}><b>{r.title}</b><span>{r.date} · {r.type}</span></div>)}</Card></div></> }

function SettingsPage({ page }: { page: Page }) { const isSource = page === 'sources'; const isRules = page === 'rules'; return <Card><div className="eyebrow">ADMIN ONLY</div><h2>{pageMeta[page][0]}</h2><p className="settings-intro">所有配置修改均写入机构审计日志，并由相应权限范围控制。</p>{isSource && <><Setting title="WeKnora 机构研究知识库" detail="已连接 · 1,284 个文档 · 最近同步 8 分钟前" enabled /><Setting title="研究邮箱附件入库" detail="首期关闭；保留后续授权接入位" /><Setting title="网盘目录增量同步" detail="首期关闭；仅允许白名单目录" /></>}{isRules && <><Setting title="多维共振主动提示" detail="≥ 2 个显著风险维度同时触发" enabled /><Setting title="最高优先级" detail="4 个风险维度同时异常" enabled /><Setting title="自动化交易执行" detail="硬性禁用：风险模块不得连接下单 API" /></>}{page === 'audit' && <table><thead><tr><th>成员</th><th>角色</th><th>可访问范围</th><th>最近活动</th></tr></thead><tbody><tr><td><b>林然</b></td><td>研究负责人</td><td>全部研究与裁决</td><td>14:35 · 确认风险提示</td></tr><tr><td><b>陈可</b></td><td>研究员</td><td>加密、宏观</td><td>13:12 · 上传报告</td></tr><tr><td><b>Alex</b></td><td>观察者</td><td>已发布研究</td><td>昨日 17:40 · 阅读</td></tr></tbody></table>}</Card> }
function Setting({ title, detail, enabled = false }: {title:string, detail:string, enabled?:boolean}) { const [on, setOn] = useState(enabled); return <div className="setting"><div><b>{title}</b><p>{detail}</p></div><button onClick={() => setOn(!on)} className={`toggle ${on ? 'on' : ''}`}><i /></button></div> }

function App() { const [page, setPage] = useState<Page>('ask'); const [collapsed, setCollapsed] = useState(false); const render = () => ({ask:<AskPage />, memory:<MemoryPage />, studio:<StudioPage />, risk:<RiskPage />, watchlist:<WatchlistPage />, cockpit:<CockpitPage />, sources:<SettingsPage page="sources" />, rules:<SettingsPage page="rules" />, audit:<SettingsPage page="audit" />}[page]); const [title, subtitle] = pageMeta[page]; return <div className={`app-shell ${collapsed ? 'collapsed' : ''}`}><aside><div className="brand"><span>R</span><div><b>RESEARCH OS</b><small>机构研究工作台</small></div><button onClick={() => setCollapsed(!collapsed)}>☰</button></div><nav>{navGroups.map(group => <div className="nav-group" key={group.label}><label>{group.label}</label>{group.items.map(([id, icon, text]) => <button key={id} onClick={() => setPage(id)} className={page === id ? 'active' : ''}><i>{icon}</i><span>{text}</span>{id === 'risk' && <em>2</em>}</button>)}</div>)}</nav><div className="user"><div className="avatar">LR</div><div><b>林然</b><small>研究负责人</small></div><span>⌄</span></div></aside><main><header><div><div className="breadcrumb">RESEARCH OS / {title}</div><h1>{title}</h1><p>{subtitle}</p></div><div className="header-actions"><button className="notice">◉<span>2</span></button><button className="status"><i /> WeKnora 已连接</button></div></header><div className="page-content">{render()}</div></main></div> }

export default App
