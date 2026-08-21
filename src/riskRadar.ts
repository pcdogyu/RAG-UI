export type RiskRange = '4h' | '24h' | '7d'

export const ethRiskSymbol = 'ETH-USDT'
export const riskRanges: RiskRange[] = ['4h', '24h', '7d']

export function directionalLevels<T extends { side: string }>(levels: T[], side: 'long' | 'short'): T[] {
  return levels.filter(level => level.side === side)
}
