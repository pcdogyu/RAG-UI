import DOMPurify from 'dompurify'

const allowedTags = ['a', 'b', 'blockquote', 'br', 'code', 'div', 'em', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'i', 'li', 'ol', 'p', 'pre', 'span', 'strong', 'sub', 'sup', 'table', 'tbody', 'td', 'th', 'thead', 'tr', 'ul']
const allowedTagNames = new Set(allowedTags)
const blockedTagNames = new Set(['audio', 'button', 'embed', 'form', 'iframe', 'img', 'input', 'link', 'math', 'meta', 'object', 'script', 'source', 'style', 'svg', 'template', 'video'])

function safeHTTPURL(value: string | null): string | null {
  const raw = value?.trim()
  if (!raw || !/^https?:\/\//i.test(raw)) return null
  try {
    const parsed = new URL(raw)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : null
  } catch {
    return null
  }
}

function replaceCustomTags(document: Document) {
  for (const web of Array.from(document.body.querySelectorAll('web'))) {
    const url = safeHTTPURL(web.getAttribute('url'))
    const title = web.getAttribute('title')?.trim() || web.textContent?.trim() || web.getAttribute('url')?.trim() || '网页引用'
    const replacement = document.createElement(url ? 'a' : 'span')
    replacement.className = url ? 'answer-web-link' : 'answer-tag-fallback'
    replacement.textContent = url ? title : `〔网页引用：${title}〕`
    if (url) {
      replacement.setAttribute('href', url)
      replacement.setAttribute('target', '_blank')
      replacement.setAttribute('rel', 'noopener noreferrer')
      replacement.setAttribute('title', url)
    }
    web.replaceWith(replacement)
  }

  for (const kb of Array.from(document.body.querySelectorAll('kb'))) {
    const title = kb.getAttribute('doc')?.trim() || kb.getAttribute('title')?.trim()
    const replacement = document.createElement('span')
    replacement.className = 'answer-internal-reference'
    replacement.textContent = title ? `〔内部资料：${title}〕` : '〔内部资料〕'
    kb.replaceWith(replacement)
  }

  for (const element of Array.from(document.body.querySelectorAll('*'))) {
    const tagName = element.tagName.toLowerCase()
    if (allowedTagNames.has(tagName)) continue
    if (blockedTagNames.has(tagName)) {
      element.remove()
      continue
    }
    const label = element.getAttribute('title')?.trim() || element.getAttribute('label')?.trim() || element.textContent?.trim()
    const replacement = document.createElement('span')
    replacement.className = 'answer-tag-fallback'
    replacement.textContent = `〔${tagName}${label ? `：${label}` : ''}〕`
    element.replaceWith(replacement)
  }
}

export function renderResearchAnswerHtml(answer: string): string {
  const document = new DOMParser().parseFromString(answer, 'text/html')
  replaceCustomTags(document)
  const sanitized = DOMPurify.sanitize(document.body.innerHTML, {
    ALLOWED_TAGS: allowedTags,
    ALLOWED_ATTR: ['class', 'colspan', 'href', 'rel', 'rowspan', 'target', 'title'],
    ALLOWED_URI_REGEXP: /^https?:\/\//i,
    FORBID_ATTR: ['style'],
    RETURN_TRUSTED_TYPE: false,
  })
  const output = new DOMParser().parseFromString(sanitized, 'text/html')
  for (const link of Array.from(output.body.querySelectorAll('a'))) {
    const url = safeHTTPURL(link.getAttribute('href'))
    if (!url) {
      link.removeAttribute('href')
      link.removeAttribute('target')
      link.removeAttribute('rel')
      continue
    }
    link.setAttribute('href', url)
    link.setAttribute('target', '_blank')
    link.setAttribute('rel', 'noopener noreferrer')
  }
  return output.body.innerHTML
}
