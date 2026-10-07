import type MarkdownIt from 'markdown-it'

export const MARKDOWN_TABLE_SCROLL_CLASS = 'markdown-table-scroll'

type RenderRule = NonNullable<MarkdownIt['renderer']['rules']['table_open']>

const renderDefault: RenderRule = (tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options)

/**
 * Wraps rendered tables in a horizontally scrollable container so wide tables
 * don't overflow their surroundings. A wrapper is used instead of
 * `display: block` on the table itself, which strips table semantics for
 * assistive technology.
 */
export function markdownTableScroll(md: MarkdownIt): void {
  const renderOpen = md.renderer.rules.table_open ?? renderDefault
  const renderClose = md.renderer.rules.table_close ?? renderDefault
  const wrapOpen: RenderRule = (...args) => `<div class="${MARKDOWN_TABLE_SCROLL_CLASS}">\n${renderOpen(...args)}`
  const wrapClose: RenderRule = (...args) => `${renderClose(...args)}</div>\n`
  md.renderer.rules.table_open = wrapOpen
  md.renderer.rules.table_close = wrapClose
}

/** Plugins shared by every rendered-markdown view in the UI. */
export const markdownPlugins = [markdownTableScroll]