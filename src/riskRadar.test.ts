import { describe, expect, it } from 'vitest'
import { directionalLevels, ethRiskSymbol, riskRanges } from './riskRadar'

describe('ETH risk radar view model', () => {
  it('keeps the radar fixed to ETH and exposes the supported chart ranges', () => {
    expect(ethRiskSymbol).toBe('ETH-USDT')
    expect(riskRanges).toEqual(['4h', '24h', '7d'])
  })

  it('separates long and short liquidation levels for rendering', () => {
    const levels = [{ side: 'long', price: 2200 }, { side: 'short', price: 2500 }, { side: 'long', price: 2100 }]
    expect(directionalLevels(levels, 'long').map(item => item.price)).toEqual([2200, 2100])
    expect(directionalLevels(levels, 'short').map(item => item.price)).toEqual([2500])
  })
})
