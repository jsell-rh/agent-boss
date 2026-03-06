import { marked } from 'marked'
import DOMPurify from 'dompurify'

// Configure marked for agent messages: breaks converts \n to <br> inside paragraphs
marked.use({ breaks: true, gfm: true })

const BLOCK_ALLOWED_TAGS = [
  'p', 'br', 'strong', 'em', 'code', 'pre', 'ul', 'ol', 'li',
  'blockquote', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'hr', 'del', 's',
]
const INLINE_ALLOWED_TAGS = ['strong', 'em', 'code', 'a', 'del', 's', 'br']
const ALLOWED_ATTR = ['href', 'target', 'rel', 'class']

export function renderMarkdown(text: string): string {
  if (!text) return ''
  const html = marked.parse(text) as string
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: BLOCK_ALLOWED_TAGS,
    ALLOWED_ATTR,
  })
}

export function renderMarkdownInline(text: string): string {
  if (!text) return ''
  const html = marked.parseInline(text) as string
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: INLINE_ALLOWED_TAGS,
    ALLOWED_ATTR,
  })
}
