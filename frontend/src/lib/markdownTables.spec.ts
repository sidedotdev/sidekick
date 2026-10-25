import { describe, it, expect } from 'vitest'
import MarkdownIt from 'markdown-it'
import { markdownTableScroll, MARKDOWN_TABLE_SCROLL_CLASS } from './markdownTables'

const render = (source: string) => {
  const doc = document.createElement('div')
  doc.innerHTML = new MarkdownIt().use(markdownTableScroll).render(source)
  return doc
}

describe('markdownTableScroll', () => {
  it('wraps each table in a scroll container while keeping table semantics', () => {
    const doc = render('| a | b |\n| - | - |\n| 1 | 2 |\n\ntext\n\n| c |\n| - |\n| 3 |\n')
    const wrappers = doc.querySelectorAll(`.${MARKDOWN_TABLE_SCROLL_CLASS}`)
    expect(wrappers).toHaveLength(2)
    wrappers.forEach((wrapper) => {
      expect(wrapper.children).toHaveLength(1)
      expect(wrapper.firstElementChild?.tagName).toBe('TABLE')
    })
    expect(doc.querySelectorAll('thead th')).toHaveLength(3)
    expect(doc.querySelector('p')?.parentElement).toBe(doc)
  })

  it('preserves column alignment from the markdown source', () => {
    const doc = render('| l | c | r |\n| :- | :-: | -: |\n| 1 | 2 | 3 |\n')
    const aligns = Array.from(doc.querySelectorAll('tbody td')).map((td) => (td as HTMLElement).style.textAlign)
    expect(aligns).toEqual(['left', 'center', 'right'])
  })

  it('leaves non-table markdown untouched', () => {
    const source = '# Title\n\nSome `code` and a | pipe.\n'
    expect(new MarkdownIt().use(markdownTableScroll).render(source)).toBe(new MarkdownIt().render(source))
  })
})