import { describe, expect, it } from 'vitest'
import { renderResearchAnswerHtml } from './researchAnswerHtml'

const parse = (html: string) => new DOMParser().parseFromString(html, 'text/html')

describe('renderResearchAnswerHtml', () => {
  it('turns a HYGR web reference into a safe external link', () => {
    const document = parse(renderResearchAnswerHtml('<web url="https://example.com/report" title="外部研究报告" />'))
    const link = document.querySelector('a.answer-web-link')
    expect(link?.textContent).toBe('外部研究报告')
    expect(link?.getAttribute('href')).toBe('https://example.com/report')
    expect(link?.getAttribute('target')).toBe('_blank')
    expect(link?.getAttribute('rel')).toBe('noopener noreferrer')
  })

  it('keeps approved rich-text structure', () => {
    const document = parse(renderResearchAnswerHtml('<h3>结论</h3><ul><li><strong>核心观点</strong></li></ul><blockquote>关注风险</blockquote>'))
    expect(document.querySelector('h3')?.textContent).toBe('结论')
    expect(document.querySelector('ul li strong')?.textContent).toBe('核心观点')
    expect(document.querySelector('blockquote')?.textContent).toBe('关注风险')
  })

  it('removes executable markup and unsafe links', () => {
    const html = renderResearchAnswerHtml('<script>alert(1)</script><a href="javascript:alert(1)" onclick="alert(1)">危险链接</a><web url="javascript:alert(1)" title="非法引用" />')
    expect(html).not.toContain('script')
    expect(html).not.toContain('onclick')
    expect(html).not.toContain('javascript:')
    expect(html).toContain('〔网页引用：非法引用〕')
  })

  it('keeps an unsupported custom tag as readable text', () => {
    const document = parse(renderResearchAnswerHtml('<citation title="未支持引用" />'))
    expect(document.body.textContent).toContain('〔citation：未支持引用〕')
  })
})
