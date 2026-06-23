import { describe, it, expect, afterEach } from 'vitest'
import { mount, flushPromises, VueWrapper } from '@vue/test-utils'
import { foldedRanges } from '@codemirror/language'
import type { EditorView } from 'codemirror'
import IntentMarkdownEditor from '../IntentMarkdownEditor.vue'

const sampleWithFrontmatter = `---
intent_links:
  - intent: "#some-section"
    code:
      - foo/bar.go:Baz
---
# Heading

Body paragraph.
`

const sampleWithoutFrontmatter = `# Heading

Body paragraph.
`

const getView = (wrapper: VueWrapper): EditorView | null => {
  const exposed = wrapper.vm as unknown as {
    __editorViewForTest?: () => EditorView | null
  }
  return exposed.__editorViewForTest?.() ?? null
}

const collectFoldedRanges = (view: EditorView): Array<{ from: number; to: number }> => {
  const ranges: Array<{ from: number; to: number }> = []
  const set = foldedRanges(view.state)
  const iter = set.iter()
  while (iter.value) {
    ranges.push({ from: iter.from, to: iter.to })
    iter.next()
  }
  return ranges
}

describe('IntentMarkdownEditor', () => {
  let wrapper: VueWrapper | null = null

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
  })

  it('folds YAML frontmatter by default on mount', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: sampleWithFrontmatter, committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view, 'editor view should be created').not.toBeNull()
    const ranges = collectFoldedRanges(view!)
    expect(ranges.length).toBe(1)

    const folded = view!.state.doc.sliceString(ranges[0].from, ranges[0].to)
    expect(folded).toContain('intent_links')
    expect(folded).toContain('foo/bar.go:Baz')
  })

  it('re-folds frontmatter when the bound document is replaced', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: sampleWithoutFrontmatter, committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    let view = getView(wrapper)
    expect(view).not.toBeNull()
    expect(collectFoldedRanges(view!)).toHaveLength(0)

    await wrapper.setProps({ modelValue: sampleWithFrontmatter })
    await flushPromises()

    view = getView(wrapper)
    const ranges = collectFoldedRanges(view!)
    expect(ranges).toHaveLength(1)
    const folded = view!.state.doc.sliceString(ranges[0].from, ranges[0].to)
    expect(folded).toContain('intent_links')
  })

  it('does not fold anything when the document has no frontmatter', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: sampleWithoutFrontmatter, committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()
    expect(collectFoldedRanges(view!)).toHaveLength(0)
  })
})