export type Report = {
  id: string
  title: string
  category: string
  asset: string
  type: string
  date: string
  status: '当前有效' | '已更新' | '历史归档'
  summary: string
  thesis: string
  invalidation: string
  evidence: string[]
}

export const reports: Report[] = [
  {
    id: 'eth-0810', title: 'ETH 8 月综合研判', category: '加密 / 链上 / 资金', asset: 'ETH', type: '深度报告', date: '2026-08-10', status: '当前有效',
    summary: 'BTC 风险开关、清算磁区与宏观流动性共同决定 ETH 的短期波动弹性。',
    thesis: '中期结构未破坏，但短线对杠杆去化更敏感。', invalidation: '现货持续流出且 ETF 净流入反转超过 3 日。',
    evidence: ['Deribit 未平仓合约与清算热力图 · 08-10 18:00', 'ETF flow 日度净流入 · 08-10', '机构内部周度策略会纪要 · 08-09'],
  },
  {
    id: 'nvda-0808', title: 'NVDA 财报前预期差跟踪', category: '公司 / 财报 / 治理', asset: 'NVDA', type: '事件快评', date: '2026-08-08', status: '当前有效',
    summary: '业绩预期维持偏强，估值已反映部分上行；关注 Capex 指引能否继续抬升。',
    thesis: '基本面强化，但预期差正在收窄。', invalidation: '云厂商 Capex 指引同步下修。',
    evidence: ['四家云厂商财报电话会 · Q2', '卖方一致预期追踪 · 08-08'],
  },
  {
    id: 'optics-0806', title: 'AI 光互联产业链更新', category: '行业 / 主题 / 产业链', asset: 'AI 光通信', type: '行业报告', date: '2026-08-06', status: '当前有效',
    summary: '核心客户资本开支预期继续上修，一阶受益环节景气度增强。',
    thesis: '800G 向 1.6T 切换仍是产业链主线。', invalidation: '订单交付与 Capex 指引出现连续背离。',
    evidence: ['供应链访谈纪要 · 08-05', '北美云厂商资本开支统计 · Q2'],
  },
  {
    id: 'eth-0618', title: 'ETH 中期展望', category: '加密 / 链上 / 资金', asset: 'ETH', type: '深度报告', date: '2026-06-18', status: '已更新',
    summary: '质押解锁节奏是 ETH 中期供给压力的核心变量。', thesis: '供给扰动将压制反弹高度。', invalidation: '新增质押量持续高于解锁量。',
    evidence: ['链上质押数据 · 06-18', '内部月度资产配置会纪要 · 06-17'],
  },
]

export const navGroups = [
  { label: '研究中心', items: [['ask', '◈', '智能问答'], ['memory', '▣', '研究记忆'], ['studio', '◒', '研究工作区']] },
  { label: '市场与风险', items: [['risk', '△', '风险雷达'], ['watchlist', '◎', '关注池']] },
  { label: '工作台', items: [['cockpit', '◌', '研究驾驶舱']] },
  { label: '管理中心', items: [['sources', '⌘', '知识库与数据接入'], ['rules', '≋', '风险规则配置'], ['audit', '◷', '成员权限与审计']] },
] as const
