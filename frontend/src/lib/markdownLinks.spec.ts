import { describe, expect, it } from 'vitest'
import MarkdownIt from 'markdown-it'
import { markdownLinks } from './markdownLinks'

const render = (source: string) => new MarkdownIt({ breaks: true }).use(markdownLinks).render(source)

describe('markdownLinks', () => {
  it.each([
    ['bare url', '(from https://example.com/docs/page)', 'https://example.com/docs/page', 'https://example.com/docs/page'],
    ['explicit link', 'See [the app](https://example.com/app).', 'https://example.com/app', 'the app'],
    ['autolink', '<https://example.com/auto>', 'https://example.com/auto', 'https://example.com/auto'],
  ])('renders %s as a link opening in a new tab', (_name, source, href, text) => {
    const container = document.createElement('div')
    container.innerHTML = render(source)
    const link = container.querySelector('a')
    expect(link).not.toBeNull()
    expect(link!.getAttribute('href')).toBe(href)
    expect(link!.textContent).toBe(text)
    expect(link!.getAttribute('target')).toBe('_blank')
    expect(link!.getAttribute('rel')).toBe('noopener noreferrer')
  })

  it('does not linkify urls inside inline code', () => {
    const container = document.createElement('div')
    container.innerHTML = render('run `curl https://example.com`')
    expect(container.querySelector('a')).toBeNull()
  })
})