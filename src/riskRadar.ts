export type RiskRange = '1h' | '4h' | '12h' | '24h' | '7d'

export const ethRiskSymbol = 'ETH-USDT'
export const riskRanges: RiskRange[] = ['1h', '4h', '12h', '24h', '7d']

export function directionalLevels<T extends { side: string }>(levels: T[], side: 'long' | 'short'): T[] {
  return levels.filter(level => level.side === side)
}
