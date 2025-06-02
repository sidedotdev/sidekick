import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import DiffFile from '../DiffFile.vue'
import type { ParsedDiff } from '../../lib/diffUtils'

// Mock DiffView component to avoid canvas rendering issues in tests
vi.mock('@git-diff-view/vue', () => ({
  DiffView: {
    name: 'DiffView',
    props: ['data', 'diff-view-font-size', 'diff-view-mode', 'diff-view-highlight', 'diff-view-add-widget', 'diff-view-wrap', 'diff-view-theme'],
    template: '<div class="mocked-diff-view">Diff content</div>'
  },
  DiffModeEnum: {
    Unified: 'unified'
  }
}))

// Mock the clipboard API
Object.assign(navigator, {
  clipboard: {
    writeText: vi.fn()
  }
})

// Mock window.matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(query => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})

describe('DiffFile', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    vi.clearAllMocks()
  })

  const createMockParsedDiff = (overrides = {}): ParsedDiff => ({
    oldFile: { fileName: 'test.js', fileLang: 'javascript' },
    newFile: { fileName: 'test.js', fileLang: 'javascript' },
    hunks: ['@@ -1,3 +1,4 @@\n console.log("hello")\n+console.log("world")\n console.log("end")'],
    linesAdded: 5,
    linesRemoved: 2,
    linesUnchanged: 10,
    ...overrides
  })

  const mountComponent = (fileData: ParsedDiff, defaultExpanded = false) => {
    wrapper = mount(DiffFile, {
      props: { fileData, defaultExpanded },
      global: {
        stubs: {
          DiffView: {
            template: '<div class="mocked-diff-view">Diff content</div>'
          }
        }
      }
    })
  }

  it('renders without errors', () => {
    const fileData = createMockParsedDiff()
    mountComponent(fileData)
    expect(wrapper.exists()).toBe(true)
  })

  it('displays the correct file path', () => {
    const fileData = createMockParsedDiff({
      newFile: { fileName: 'src/components/Test.vue', fileLang: 'vue' }
    })
    mountComponent(fileData)
    
    const filePath = wrapper.find('.file-path')
    expect(filePath.text()).toBe('src/components/Test.vue')
  })

  it('falls back to old file name when new file name is null', () => {
    const fileData = createMockParsedDiff({
      oldFile: { fileName: 'old-file.js', fileLang: 'javascript' },
      newFile: { fileName: null, fileLang: null }
    })
    mountComponent(fileData)
    
    const filePath = wrapper.find('.file-path')
    expect(filePath.text()).toBe('old-file.js')
  })

  it('displays "Unknown file" when both file names are null', () => {
    const fileData = createMockParsedDiff({
      oldFile: { fileName: null, fileLang: null },
      newFile: { fileName: null, fileLang: null }
    })
    mountComponent(fileData)
    
    const filePath = wrapper.find('.file-path')
    expect(filePath.text()).toBe('Unknown file')
  })

  it('displays correct line counts', () => {
    const fileData = createMockParsedDiff({
      linesAdded: 15,
      linesRemoved: 8
    })
    mountComponent(fileData)
    
    const addedCount = wrapper.find('.added-count')
    const removedCount = wrapper.find('.removed-count')
    
    expect(addedCount.text()).toBe('+15')
    expect(removedCount.text()).toBe('-8')
  })

  it('does not display line counts when they are zero', () => {
    const fileData = createMockParsedDiff({
      linesAdded: 0,
      linesRemoved: 0
    })
    mountComponent(fileData)
    
    const addedCount = wrapper.find('.added-count')
    const removedCount = wrapper.find('.removed-count')
    
    expect(addedCount.exists()).toBe(false)
    expect(removedCount.exists()).toBe(false)
  })

  it('calculates visual summary correctly with proportional squares', () => {
    const fileData = createMockParsedDiff({
      linesAdded: 10,
      linesRemoved: 5,
      linesUnchanged: 5
    })
    mountComponent(fileData)
    
    const squares = wrapper.findAll('.summary-square')
    expect(squares).toHaveLength(5)
    
    // With 10 added, 5 removed, 5 unchanged (total 20)
    // Ratios: 50% added, 25% removed, 25% unchanged
    // Rounded to 20%: 3 added, 1 removed, 1 unchanged
    const addedSquares = wrapper.findAll('.summary-square.added')
    const removedSquares = wrapper.findAll('.summary-square.removed')
    const unchangedSquares = wrapper.findAll('.summary-square.unchanged')
    
    expect(addedSquares).toHaveLength(3)
    expect(removedSquares).toHaveLength(1)
    expect(unchangedSquares).toHaveLength(1)
  })

  it('handles edge case with zero total lines', () => {
    const fileData = createMockParsedDiff({
      linesAdded: 0,
      linesRemoved: 0,
      linesUnchanged: 0
    })
    mountComponent(fileData)
    
    const squares = wrapper.findAll('.summary-square')
    expect(squares).toHaveLength(5)
    
    const unchangedSquares = wrapper.findAll('.summary-square.unchanged')
    expect(unchangedSquares).toHaveLength(5)
  })

  it('starts collapsed by default', () => {
    const fileData = createMockParsedDiff()
    mountComponent(fileData)
    
    const diffContent = wrapper.find('.diff-content')
    expect(diffContent.exists()).toBe(false)
    
    const expandIcon = wrapper.find('.expand-icon')
    expect(expandIcon.classes()).not.toContain('expanded')
  })

  it('starts expanded when defaultExpanded is true', () => {
    const fileData = createMockParsedDiff()
    mountComponent(fileData, true)
    
    const diffContent = wrapper.find('.diff-content')
    expect(diffContent.exists()).toBe(true)
    
    const expandIcon = wrapper.find('.expand-icon')
    expect(expandIcon.classes()).toContain('expanded')
  })

  it('toggles expansion when header is clicked', async () => {
    const fileData = createMockParsedDiff()
    mountComponent(fileData)
    
    await wrapper.vm.$nextTick()
    
    const fileHeader = wrapper.find('.file-header')
    expect(fileHeader.exists()).toBe(true)
    
    // Initially collapsed
    expect(wrapper.find('.diff-content').exists()).toBe(false)
    expect(wrapper.find('.expand-icon').classes()).not.toContain('expanded')
    
    // Click to expand
    await fileHeader.trigger('click')
    await wrapper.vm.$nextTick()
    
    expect(wrapper.find('.diff-content').exists()).toBe(true)
    expect(wrapper.find('.expand-icon').classes()).toContain('expanded')
    
    // Click to collapse
    await fileHeader.trigger('click')
    await wrapper.vm.$nextTick()
    
    expect(wrapper.find('.diff-content').exists()).toBe(false)
    expect(wrapper.find('.expand-icon').classes()).not.toContain('expanded')
  })

  it('copies file path to clipboard when copy button is clicked', async () => {
    const fileData = createMockParsedDiff({
      newFile: { fileName: 'src/test.js', fileLang: 'javascript' }
    })
    mountComponent(fileData)
    
    await wrapper.vm.$nextTick()
    
    const copyButton = wrapper.find('.copy-button')
    expect(copyButton.exists()).toBe(true)
    
    await copyButton.trigger('click')
    
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('src/test.js')
  })

  it('prevents header click when copy button is clicked', async () => {
    const fileData = createMockParsedDiff()
    mountComponent(fileData)
    
    await wrapper.vm.$nextTick()
    
    const copyButton = wrapper.find('.copy-button')
    expect(copyButton.exists()).toBe(true)
    
    // Initially collapsed
    expect(wrapper.find('.diff-content').exists()).toBe(false)
    
    // Click copy button (should not expand)
    await copyButton.trigger('click')
    await wrapper.vm.$nextTick()
    
    expect(wrapper.find('.diff-content').exists()).toBe(false)
  })

  it('handles clipboard write failure gracefully', async () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const clipboardError = new Error('Clipboard not available')
    vi.mocked(navigator.clipboard.writeText).mockRejectedValue(clipboardError)
    
    const fileData = createMockParsedDiff()
    mountComponent(fileData)
    
    await wrapper.vm.$nextTick()
    
    const copyButton = wrapper.find('.copy-button')
    expect(copyButton.exists()).toBe(true)
    
    await copyButton.trigger('click')
    
    expect(consoleSpy).toHaveBeenCalledWith('Failed to copy file path:', clipboardError)
    
    consoleSpy.mockRestore()
  })
})