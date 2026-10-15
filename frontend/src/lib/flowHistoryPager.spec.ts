import { afterEach, describe, expect, it, vi } from 'vitest'
import { createFlowHistoryPager } from './flowHistoryPager'

afterEach(() => vi.unstubAllGlobals())

describe('flow history pager', () => {
  it('loads one page, follows its cursor, and stops at the end', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(Response.json({ flowActions: [{ id: 'new' }], nextCursor: 'older' }))
      .mockResolvedValueOnce(Response.json({ flowActions: [{ id: 'old' }], nextCursor: '' }))
    vi.stubGlobal('fetch', fetcher)
    const merge = vi.fn()
    const pager = createFlowHistoryPager<{ id: string }>('actions', 'flowActions', merge)
    pager.reset('ws', 'flow')
    await pager.load()
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0][0]).toBe('/api/v1/workspaces/ws/flows/flow/actions?limit=50')
    expect(pager.hasMore.value).toBe(true)
    await pager.load()
    expect(fetcher.mock.calls[1][0]).toContain('before=older')
    expect(merge.mock.calls).toEqual([[[{ id: 'new' }]], [[{ id: 'old' }]]])
    expect(pager.hasMore.value).toBe(false)
    await pager.load()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('deduplicates in-flight requests and ignores responses from a previous flow', async () => {
    let resolve!: (response: Response) => void
    const fetcher = vi.fn().mockImplementation(() => new Promise<Response>(r => { resolve = r }))
    vi.stubGlobal('fetch', fetcher)
    const merge = vi.fn()
    const pager = createFlowHistoryPager('subflows', 'subflows', merge)
    pager.reset('ws', 'first')
    const request = pager.load()
    await pager.load()
    expect(fetcher).toHaveBeenCalledTimes(1)
    pager.reset('ws', 'second')
    resolve(Response.json({ subflows: [{ id: 'stale' }], nextCursor: '' }))
    await request
    expect(merge).not.toHaveBeenCalled()
    expect(pager.hasMore.value).toBe(true)
    expect(pager.loading.value).toBe(false)
  })

  it('keeps the cursor on failure so an older-page request can be retried', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(Response.json({ subflows: [], nextCursor: 'retry' }))
      .mockResolvedValueOnce(new Response('', { status: 500 }))
      .mockResolvedValueOnce(Response.json({ subflows: [], nextCursor: '' }))
    vi.stubGlobal('fetch', fetcher)
    const pager = createFlowHistoryPager('subflows', 'subflows', vi.fn())
    pager.reset('ws', 'flow')
    await pager.load()
    await pager.load()
    expect(pager.error.value).toBeTruthy()
    expect(pager.hasMore.value).toBe(true)
    await pager.load()
    expect(fetcher.mock.calls[2][0]).toBe(fetcher.mock.calls[1][0])
    expect(pager.error.value).toBeNull()
    expect(pager.hasMore.value).toBe(false)
  })
})