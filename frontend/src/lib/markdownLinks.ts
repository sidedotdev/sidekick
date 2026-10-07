import type MarkdownIt from 'markdown-it'

/**
 * Autolinks bare URLs and opens every link in a new tab, since rendered
 * markdown is embedded within app views that shouldn't be navigated away from.
 */
export function markdownLinks(md: MarkdownIt): void {
  md.set({ linkify: true })
  const renderLinkOpen = md.renderer.rules.link_open
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
    tokens[idx].attrSet('target', '_blank')
    tokens[idx].attrSet('rel', 'noopener noreferrer')
    return renderLinkOpen
      ? renderLinkOpen(tokens, idx, options, env, self)
      : self.renderToken(tokens, idx, options)
  }
}