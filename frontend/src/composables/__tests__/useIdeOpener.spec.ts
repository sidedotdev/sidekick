import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useIdeOpener } from '../useIdeOpener'

describe('useIdeOpener', () => {
  const mockSessionStorage: Record<string, string> = {}
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let windowOpenSpy: any

  beforeEach(() => {
    vi.clearAllMocks()
    
    // Clear mock storage
    Object.keys(mockSessionStorage).forEach(key => delete mockSessionStorage[key])
    
    // Mock sessionStorage
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation((key: string) => {
      return mockSessionStorage[key] || null
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation((key: string, value: string) => {
      mockSessionStorage[key] = value
    })
    
    // Mock window.open
    windowOpenSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
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
      expect(windowOpenSpy).not.toHaveBeenCalled()
    })

    it('opens file directly in VSCode when preference is stored (no baseDir)', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { showIdeSelector, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      
      expect(showIdeSelector.value).toBe(false)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//path/to/file.ts?windowId=_blank',
        '_self'
      )
    })

    it('opens file directly in IntelliJ when preference is stored', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { showIdeSelector, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      
      expect(showIdeSelector.value).toBe(false)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'idea://open?file=%2Fpath%2Fto%2Ffile.ts',
        '_self'
      )
    })

    it('opens VSCode with two-step flow when baseDir is provided', async () => {
      vi.useFakeTimers()
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(1)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//workspace?windowId=_blank',
        '_self'
      )
      
      await vi.advanceTimersByTimeAsync(250)
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(2)
      expect(windowOpenSpy).toHaveBeenLastCalledWith(
        'vscode://file//workspace/path/to/file.ts?windowId=_blank',
        '_self'
      )
      
      vi.useRealTimers()
    })

    it('opens VSCode with two-step flow including line number when baseDir is provided', async () => {
      vi.useFakeTimers()
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', 42, '/workspace')
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(1)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//workspace?windowId=_blank',
        '_self'
      )
      
      await vi.advanceTimersByTimeAsync(250)
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(2)
      expect(windowOpenSpy).toHaveBeenLastCalledWith(
        'vscode://file//workspace/path/to/file.ts:42?windowId=_blank',
        '_self'
      )
      
      vi.useRealTimers()
    })

    it('opens IntelliJ without two-step flow even when baseDir is provided', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { openInIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(1)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'idea://open?file=%2Fworkspace%2Fpath%2Fto%2Ffile.ts',
        '_self'
      )
    })

    it('ignores invalid stored preference and shows selector', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'invalid-ide'
      const { showIdeSelector, pendingFilePath, openInIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      
      expect(showIdeSelector.value).toBe(true)
      expect(pendingFilePath.value).toBe('/path/to/file.ts')
      expect(windowOpenSpy).not.toHaveBeenCalled()
    })
  })

  describe('selectIde', () => {
    it('stores VSCode preference and opens pending file (no baseDir)', () => {
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      selectIde('vscode')
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//path/to/file.ts?windowId=_blank',
        '_self'
      )
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
    })

    it('stores VSCode preference and opens pending file with two-step flow when baseDir was provided', async () => {
      vi.useFakeTimers()
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/workspace/path/to/file.ts', null, '/workspace')
      selectIde('vscode')
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(windowOpenSpy).toHaveBeenCalledTimes(1)
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//workspace?windowId=_blank',
        '_self'
      )
      
      await vi.advanceTimersByTimeAsync(250)
      
      expect(windowOpenSpy).toHaveBeenCalledTimes(2)
      expect(windowOpenSpy).toHaveBeenLastCalledWith(
        'vscode://file//workspace/path/to/file.ts?windowId=_blank',
        '_self'
      )
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
      
      vi.useRealTimers()
    })

    it('stores IntelliJ preference and opens pending file', () => {
      const { showIdeSelector, pendingFilePath, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/path/to/file.ts')
      selectIde('intellij')
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('intellij')
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'idea://open?file=%2Fpath%2Fto%2Ffile.ts',
        '_self'
      )
      expect(showIdeSelector.value).toBe(false)
      expect(pendingFilePath.value).toBe(null)
    })

    it('does not open file if no pending file path', () => {
      const { selectIde } = useIdeOpener()
      
      selectIde('vscode')
      
      expect(mockSessionStorage['sidekick-preferred-ide']).toBe('vscode')
      expect(windowOpenSpy).not.toHaveBeenCalled()
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
      expect(windowOpenSpy).not.toHaveBeenCalled()
    })
  })

  describe('URL formatting', () => {
    it('correctly encodes special characters for IntelliJ', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'intellij'
      const { openInIde } = useIdeOpener()
      
      openInIde('/path/with spaces/and#special?chars.ts')
      
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'idea://open?file=%2Fpath%2Fwith%20spaces%2Fand%23special%3Fchars.ts',
        '_self'
      )
    })

    it('preserves special characters for VSCode', () => {
      mockSessionStorage['sidekick-preferred-ide'] = 'vscode'
      const { openInIde } = useIdeOpener()
      
      openInIde('/path/with spaces/file.ts')
      
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//path/with spaces/file.ts?windowId=_blank',
        '_self'
      )
    })
  })

  describe('baseDir handling', () => {
    it('stores baseDir when showing selector and uses it when IDE is selected', async () => {
      vi.useFakeTimers()
      const { showIdeSelector, openInIde, selectIde } = useIdeOpener()
      
      openInIde('/workspace/file.ts', 10, '/workspace')
      
      expect(showIdeSelector.value).toBe(true)
      expect(windowOpenSpy).not.toHaveBeenCalled()
      
      selectIde('vscode')
      
      expect(windowOpenSpy).toHaveBeenCalledWith(
        'vscode://file//workspace?windowId=_blank',
        '_self'
      )
      
      await vi.advanceTimersByTimeAsync(250)
      
      expect(windowOpenSpy).toHaveBeenLastCalledWith(
        'vscode://file//workspace/file.ts:10?windowId=_blank',
        '_self'
      )
      
      vi.useRealTimers()
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
      expect(windowOpenSpy).not.toHaveBeenCalled()
    })
  })
})