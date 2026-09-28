export type WebSearchAction = {
  title: string
  queries?: string[]
  url?: string
  pattern?: string
  status?: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function isNonemptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0
}

function isWebUrl(value: unknown): value is string {
  if (!isNonemptyString(value)) return false
  try {
    const url = new URL(value)
    return url.protocol === 'https:' || url.protocol === 'http:'
  } catch {
    return false
  }
}

export function parseWebSearchBlock(block: unknown): WebSearchAction | null {
  if (!isRecord(block) || block.type !== 'builtin_tool_use') return null
  const tool = block.builtinToolUse
  if (!isRecord(tool) || tool.name !== 'web_search' || typeof tool.arguments !== 'string') return null
  if (tool.status !== undefined && typeof tool.status !== 'string') return null

  let action: unknown
  try {
    action = JSON.parse(tool.arguments)
  } catch {
    return null
  }
  if (!isRecord(action)) return null
  const status = tool.status as string | undefined

  // Anthropic ({query}) and Google ({queries}) omit the OpenAI-style action type.
  if (action.type === 'search' || action.type === undefined) {
    if (action.query !== undefined && !isNonemptyString(action.query)) return null
    if (action.queries !== undefined &&
        (!Array.isArray(action.queries) || !action.queries.every(isNonemptyString))) return null
    const queries: string[] = Array.isArray(action.queries) ? [...action.queries] : []
    if (isNonemptyString(action.query) && !queries.includes(action.query)) queries.push(action.query)
    if (!queries.length) return null
    return { title: 'Web search', queries: [...new Set(queries)], status }
  }

  if (action.type === 'open_page' && isWebUrl(action.url)) {
    return { title: 'Open page', url: action.url, status }
  }

  if ((action.type === 'find_in_page' || action.type === 'find') &&
      isWebUrl(action.url) && isNonemptyString(action.pattern)) {
    return { title: 'Find in page', url: action.url, pattern: action.pattern, status }
  }

  return null
}

export type WebSearchResultItem = {
  url: string
  title: string
  pageAge?: string
}

export type WebSearchResults = {
  isError: boolean
  content?: string
  results: WebSearchResultItem[]
}

function parseWebSearchResultItem(value: unknown): WebSearchResultItem | null {
  if (!isRecord(value) || !isWebUrl(value.url) || typeof value.title !== 'string') return null
  if (value.pageAge !== undefined && typeof value.pageAge !== 'string') return null
  return { url: value.url, title: value.title, pageAge: value.pageAge || undefined }
}

export function parseWebSearchResultBlock(block: unknown): WebSearchResults | null {
  if (!isRecord(block) || block.type !== 'builtin_tool_result') return null
  const result = block.builtinToolResult
  if (!isRecord(result) || result.name !== 'web_search') return null
  if (result.isError !== undefined && typeof result.isError !== 'boolean') return null
  if (result.content !== undefined && typeof result.content !== 'string') return null
  if (result.searchResults !== undefined && !Array.isArray(result.searchResults)) return null

  const results: WebSearchResultItem[] = []
  for (const item of (result.searchResults as unknown[] | undefined) ?? []) {
    const parsed = parseWebSearchResultItem(item)
    if (!parsed) return null
    results.push(parsed)
  }
  return {
    isError: result.isError === true,
    content: (result.content as string | undefined) || undefined,
    results
  }
}