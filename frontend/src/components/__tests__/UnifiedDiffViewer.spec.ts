import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import UnifiedDiffViewer from '../UnifiedDiffViewer.vue'
import DiffFile from '../DiffFile.vue'

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

// Mock the parseDiff function
vi.mock('../../lib/diffUtils', () => ({
  parseDiff: vi.fn()
}))

import { parseDiff } from '../../lib/diffUtils'
const mockParseDiff = vi.mocked(parseDiff)

describe('UnifiedDiffViewer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering with valid diff strings', () => {
    it('renders single file diff correctly', () => {
      const singleFileDiff = `diff --git a/test.js b/test.js
index 1234567..abcdefg 100644
--- a/test.js
+++ b/test.js
@@ -1,3 +1,4 @@
 function test() {
+  console.log('hello');
   return true;
 }`

      const mockParsedData = [{
        oldFile: { fileName: 'test.js', fileLang: 'javascript' },
        newFile: { fileName: 'test.js', fileLang: 'javascript' },
        hunks: [singleFileDiff],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 2,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: singleFileDiff
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(mockParseDiff).toHaveBeenCalledWith(singleFileDiff)
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(1)
      expect(wrapper.findComponent(DiffFile).props('fileData')).toEqual(mockParsedData[0])
      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(false)
    })

    it('renders multiple file diffs correctly', () => {
      const multiFileDiff = `diff --git a/file1.js b/file1.js
index 1234567..abcdefg 100644
--- a/file1.js
+++ b/file1.js
@@ -1,2 +1,3 @@
 console.log('file1');
+console.log('added line');
 
diff --git a/file2.py b/file2.py
index 7890123..fedcba9 100644
--- a/file2.py
+++ b/file2.py
@@ -1,3 +1,2 @@
 def hello():
-    print('removed line')
     return True`

      const mockParsedData = [
        {
          oldFile: { fileName: 'file1.js', fileLang: 'javascript' },
          newFile: { fileName: 'file1.js', fileLang: 'javascript' },
          hunks: ['diff --git a/file1.js b/file1.js\nindex 1234567..abcdefg 100644\n--- a/file1.js\n+++ b/file1.js\n@@ -1,2 +1,3 @@\n console.log(\'file1\');\n+console.log(\'added line\');\n '],
          linesAdded: 1,
          linesRemoved: 0,
          linesUnchanged: 1,
          firstLineNumber: 1
        },
        {
          oldFile: { fileName: 'file2.py', fileLang: 'python' },
          newFile: { fileName: 'file2.py', fileLang: 'python' },
          hunks: ['diff --git a/file2.py b/file2.py\nindex 7890123..fedcba9 100644\n--- a/file2.py\n+++ b/file2.py\n@@ -1,3 +1,2 @@\n def hello():\n-    print(\'removed line\')\n     return True'],
          linesAdded: 0,
          linesRemoved: 1,
          linesUnchanged: 2,
          firstLineNumber: 1
        }
      ]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: multiFileDiff
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(mockParseDiff).toHaveBeenCalledWith(multiFileDiff)
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(2)
      
      const diffFileComponents = wrapper.findAllComponents(DiffFile)
      expect(diffFileComponents[0].props('fileData')).toEqual(mockParsedData[0])
      expect(diffFileComponents[1].props('fileData')).toEqual(mockParsedData[1])
      
      // Both should have default expanded state
      expect(diffFileComponents[0].props('defaultExpanded')).toBe(false)
      expect(diffFileComponents[1].props('defaultExpanded')).toBe(false)
    })

    it('passes custom defaultExpanded prop to DiffFile components', () => {
      const diffString = `diff --git a/test.js b/test.js
index 1234567..abcdefg 100644
--- a/test.js
+++ b/test.js
@@ -1,1 +1,2 @@
 console.log('test');
+console.log('added');`

      const mockParsedData = [{
        oldFile: { fileName: 'test.js', fileLang: 'javascript' },
        newFile: { fileName: 'test.js', fileLang: 'javascript' },
        hunks: [diffString],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString,
          defaultExpanded: true
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(true)
    })
  })

  describe('handling empty and invalid diff strings', () => {
    it('renders no-diff message for empty string', () => {
      mockParseDiff.mockReturnValue([])

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: ''
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.find('.no-diff-message').exists()).toBe(true)
      expect(wrapper.find('.no-diff-message').text()).toBe('No diff content to display')
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(0)
    })

    it('renders no-diff message for whitespace-only string', () => {
      mockParseDiff.mockReturnValue([])

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: '   \n\t  \n  '
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.find('.no-diff-message').exists()).toBe(true)
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(0)
    })

    it('handles parseDiff returning empty array', () => {
      mockParseDiff.mockReturnValue([])

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'some invalid diff content'
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.find('.no-diff-message').exists()).toBe(true)
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(0)
    })

    it('handles parseDiff throwing an error', () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      mockParseDiff.mockImplementation(() => {
        throw new Error('Parse error')
      })

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'malformed diff'
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(consoleSpy).toHaveBeenCalledWith('Failed to parse diff string:', expect.any(Error))
      expect(wrapper.find('.no-diff-message').exists()).toBe(true)
      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(0)

      consoleSpy.mockRestore()
    })
  })

  describe('prop validation and defaults', () => {
    it('uses false as default for defaultExpanded prop', () => {
      const mockParsedData = [{
        oldFile: { fileName: 'test.js', fileLang: 'javascript' },
        newFile: { fileName: 'test.js', fileLang: 'javascript' },
        hunks: ['diff content'],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'some diff'
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(false)
    })

    it('accepts boolean values for defaultExpanded prop', () => {
      const mockParsedData = [{
        oldFile: { fileName: 'test.js', fileLang: 'javascript' },
        newFile: { fileName: 'test.js', fileLang: 'javascript' },
        hunks: ['diff content'],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'some diff',
          defaultExpanded: true
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(true)
    })
  })

  describe('key generation for DiffFile components', () => {
    it('generates unique keys for files with different names', () => {
      const mockParsedData = [
        {
          oldFile: { fileName: 'file1.js', fileLang: 'javascript' },
          newFile: { fileName: 'file1.js', fileLang: 'javascript' },
          hunks: ['diff1'],
          linesAdded: 1,
          linesRemoved: 0,
          linesUnchanged: 1,
          firstLineNumber: 1
        },
        {
          oldFile: { fileName: 'file2.js', fileLang: 'javascript' },
          newFile: { fileName: 'file2.js', fileLang: 'javascript' },
          hunks: ['diff2'],
          linesAdded: 0,
          linesRemoved: 1,
          linesUnchanged: 1,
          firstLineNumber: 1
        }
      ]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'multi file diff'
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      const diffFiles = wrapper.findAllComponents(DiffFile)
      expect(diffFiles).toHaveLength(2)
      
      // Keys should be different for different files - check via DOM attributes
      const elements = diffFiles.map(component => component.element)
      expect(elements[0]).not.toBe(elements[1])
    })

    it('handles files with null names in key generation', () => {
      const mockParsedData = [{
        oldFile: { fileName: null, fileLang: null },
        newFile: { fileName: null, fileLang: null },
        hunks: ['diff content'],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: null
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'diff with unknown file'
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findAllComponents(DiffFile)).toHaveLength(1)
      // Should not throw an error when generating keys with null file names
    })
  })

  describe('reactivity', () => {
    it('re-parses diff when diffString prop changes', async () => {
      const initialDiff = 'initial diff'
      const newDiff = 'new diff content'

      const initialParsedData = [{
        oldFile: { fileName: 'initial.js', fileLang: 'javascript' },
        newFile: { fileName: 'initial.js', fileLang: 'javascript' },
        hunks: [initialDiff],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: 1
      }]

      const newParsedData = [{
        oldFile: { fileName: 'new.js', fileLang: 'javascript' },
        newFile: { fileName: 'new.js', fileLang: 'javascript' },
        hunks: [newDiff],
        linesAdded: 2,
        linesRemoved: 1,
        linesUnchanged: 0,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValueOnce(initialParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: initialDiff
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findComponent(DiffFile).props('fileData')).toEqual(initialParsedData[0])

      // Change the diff string
      mockParseDiff.mockReturnValueOnce(newParsedData)
      await wrapper.setProps({ diffString: newDiff })

      expect(mockParseDiff).toHaveBeenCalledWith(newDiff)
      expect(wrapper.findComponent(DiffFile).props('fileData')).toEqual(newParsedData[0])
    })

    it('updates when defaultExpanded prop changes', async () => {
      const mockParsedData = [{
        oldFile: { fileName: 'test.js', fileLang: 'javascript' },
        newFile: { fileName: 'test.js', fileLang: 'javascript' },
        hunks: ['diff content'],
        linesAdded: 1,
        linesRemoved: 0,
        linesUnchanged: 1,
        firstLineNumber: 1
      }]

      mockParseDiff.mockReturnValue(mockParsedData)

      const wrapper = mount(UnifiedDiffViewer, {
        props: {
          diffString: 'some diff',
          defaultExpanded: false
        },
        global: {
          stubs: {
            DiffView: {
              template: '<div class="mocked-diff-view">Diff content</div>'
            }
          }
        }
      })

      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(false)

      await wrapper.setProps({ defaultExpanded: true })

      expect(wrapper.findComponent(DiffFile).props('defaultExpanded')).toBe(true)
    })
  })
})