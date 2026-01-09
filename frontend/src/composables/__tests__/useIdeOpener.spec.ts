import { describe, it, expect, vi, beforeEach, afterEach, type MockInstance } from 'vitest'
import { useIdeOpener } from '../useIdeOpener'

describe('useIdeOpener', () => {
  const mockSessionStorage: Record<string, string> = {}
  let fetchSpy: MockInstance<[input: RequestInfo | URL, init?: RequestInit | undefined], Promise<Response>>
  let lastFetchBody: unknown = null

  beforeEach(() => {
    vi.clearAllMocks()
    lastFetchBody = null
    
    // Clear mock storage
    Object.keys(mockSessionStorage).forEach(key => delete mockSessionStorage[key])
    
    // Mock sessionStorage
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation((key: string) => {
      return mockSessionStorage[key] || null
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation((key: string, value: string) => {
      mockSessionStorage[key] = value
    })
    
    // Mock fetch
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_url, options) => {
      lastFetchBody = options?.body ? JSON.parse(options.body as string) : null
      return new Response(JSON.stringify({ success: true }), { status: 200 })
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('initializes with selector hidden and no pending file', () => {
    const { showIdeSelector, pendingFilePath } = useIdeOpener()
    
    expect(showIdeSelector.value).toBe(false)
    expect(pendingFilePath.value).toBe(null)
  })

  describe('openInIde', () => {
    it('shows selector when no IDE preference is stored', () => {
      const { showIdeSelector, pendingFilePath, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      
      expect(showIdeSelector.value).toBe(true)
      expect(pendingFilePath.value).toBe('/path/to/file.ts')
      expect(fetchSpy).not.toHaveBeenCalled()
    })

    it('opens file directly in VSCode when preference is stored (no baseDir)', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { showIdeSelector, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(showIdeSelector.value).toBe(false)
      expect(fetchSpy).toHaveBeenCalledWith('/api/v1/open-in-ide', expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      }))
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/path/to/file.ts',
      })
    })

    it('opens file directly in IntelliJ when preference is stored', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { showIdeSelector, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(showIdeSelector.value).toBe(false)
      expect(lastFetchBody).toEqual({
        ide: 'intellij',
        filePath: '/path/to/file.ts',
      })
    })

    it('opens VSCode with baseDir when provided', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/workspace/path/to/file.ts',
        baseDir: '/workspace',
      })
    })

    it('opens VSCode with line number and baseDir when provided', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', 42, '/workspace')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/workspace/path/to/file.ts',
        line: 42,
        baseDir: '/workspace',
      })
    })

    it('opens IntelliJ with baseDir when provided', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'intellij',
        filePath: '/workspace/path/to/file.ts',
        baseDir: '/workspace',
      })
    })

    it('ignores invalid stored preference and shows selector', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'invalid-ide'
      const { showIdeSelector, pendingFilePath, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      
      expect(showIdeSelector.value).toBe(true)
      expect(pendingFilePath.value).toBe('/path/to/file.ts')
      expect(fetchSpy).not.toHaveBeenCalled()
    })
  })

  describe('selectIde', () => {
    it('stores VSCode preference and opens pending file (no baseDir)', async () => {
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      selectIde('vscode')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/path/to/file.ts',
      })
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
    })

    it('stores VSCode preference and opens pending file with baseDir when provided', async () => {
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      selectIde('vscode')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/workspace/path/to/file.ts',
        baseDir: '/workspace',
      })
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
    })

    it('stores IntelliJ preference and opens pending file', async () => {
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      selectIde('intellij')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('intellij')
      expect(lastFetchBody).toEqual({
        ide: 'intellij',
        filePath: '/path/to/file.ts',
      })
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
    })

    it('does not open file if no pending file path', () => {
      const { selectIde } = useIdeOpener()
      
      selectIde('vscode')
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(fetchSpy).not.toHaveBeenCalled()
    })
  })

  describe('cancelIdeSelection', () => {
    it('hides selector and clears pending file', () => {
      const { showIdeSelector, pendingFilePath, openInIde, cancelIdeSelection } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      expect(showIdeSelector.value).toBe(true)
      expect(pendingFilePath.value).toBe('/path/to/file.ts')
      
      cancelIdeSelection()
      
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
      expect(fetchSpy).not.toHaveBeenCalled()
    })
  })

  describe('request payload', () => {
    it('sends special characters in file path to API', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { openInIde } = useIdeOpener()
      
      openInIde('/path/with spaces/and#special?chars.ts')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'intellij',
        filePath: '/path/with spaces/and#special?chars.ts',
      })
    })

    it('sends file path with spaces to API for VSCode', async () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/path/with spaces/file.ts')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/path/with spaces/file.ts',
      })
    })
  })

  describe('baseDir handling', () => {
    it('stores baseDir when showing selector and uses it when IDE is selected', async () => {
      const { showIdeSelector, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/workspace/file.ts', 10, '/workspace')
      
      expect(showIdeSelector.value).toBe(true)
      expect(fetchSpy).not.toHaveBeenCalled()
      
      selectIde('vscode')
      await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())
      
      expect(lastFetchBody).toEqual({
        ide: 'vscode',
        filePath: '/workspace/file.ts',
        line: 10,
        baseDir: '/workspace',
      })
    })

    it('clears baseDir when selection is cancelled', () => {
      const { showIdeSelector, openInIde, cancelIdeSelection, selectIde } = useIdeOpener()
      
      openInIde('/workspace/file.ts', null, '/workspace')
      expect(showIdeSelector.value).toBe(true)
      
      cancelIdeSelection()
      expect(showIdeSelector.value).toBe(false)
      
      // Now if we select an IDE, it should not use the old baseDir
      // (though there's no pending file, so nothing happens)
      selectIde('vscode')
      expect(fetchSpy).not.toHaveBeenCalled()
    })
  })
})