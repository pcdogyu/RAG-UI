import { describe, expect, it } from 'vitest'
import { activeRiskSignalCount, directionalLevels, ethRiskSymbol, riskRanges } from './riskRadar'

describe('ETH risk radar view model', () => {
  it('keeps the radar fixed to ETH and exposes the supported chart ranges', () => {
    expect(ethRiskSymbol).toBe('ETH-USDT')
    expect(riskRanges).toEqual(['1h', '4h', '12h', '24h', '7d'])
  })

  it('separates long and short liquidation levels for rendering', () => {
    const levels = [{ side: 'long', price: 2200 }, { side: 'short', price: 2500 }, { side: 'long', price: 2100 }]
    expect(directionalLevels(levels, 'long').map(item => item.price)).toEqual([2200, 2100])
    expect(directionalLevels(levels, 'short').map(item => item.price)).toEqual([2500])
  })

  it('counts current triggered factors for shared alert badges', () => {
    expect(activeRiskSignalCount([{ active: false }, { active: true }, { active: false }])).toBe(1)
    expect(activeRiskSignalCount(undefined)).toBe(0)
  })
})
