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

  it('folds the entire frontmatter section onto the first line by default', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: sampleWithFrontmatter, committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view, 'editor view should be created').not.toBeNull()
    const ranges = collectFoldedRanges(view!)
    expect(ranges.length).toBe(1)

    const firstLine = view!.state.doc.line(1)
    expect(firstLine.text).toBe('---')
    expect(ranges[0].from).toBeGreaterThanOrEqual(firstLine.to)
    expect(ranges[0].from).toBeLessThanOrEqual(view!.state.doc.line(2).from)

    const folded = view!.state.doc.sliceString(ranges[0].from, ranges[0].to)
    expect(folded).toContain('intent_links')
    expect(folded).toContain('foo/bar.go:Baz')
    expect(folded).toContain('---')
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

  const pressTab = (view: EditorView, shift: boolean) => {
    view.contentDOM.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Tab', shiftKey: shift, bubbles: true }),
    )
  }

  it('indents the selected lines on Tab and dedents them on Shift+Tab', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: 'alpha\nbeta\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    view!.dispatch({ selection: { anchor: 0, head: view!.state.doc.length } })
    pressTab(view!, false)
    expect(view!.state.doc.toString()).toBe('  alpha\n  beta\n')

    view!.dispatch({ selection: { anchor: 0, head: view!.state.doc.length } })
    pressTab(view!, true)
    expect(view!.state.doc.toString()).toBe('alpha\nbeta\n')
  })

  it('inserts spaces at the cursor on Tab when nothing is selected', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: 'alpha\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    view!.dispatch({ selection: { anchor: 0, head: 0 } })
    pressTab(view!, false)
    expect(view!.state.doc.toString()).toBe('  alpha\n')
  })

  it('reflows the document on demand via formatNow', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: '# Heading\n\n\n\none two\nthree four\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    const result = exposed.formatNow()

    expect(result).toBe('# Heading\n\none two three four\n')
    expect(view!.state.doc.toString()).toBe('# Heading\n\none two three four\n')
  })

  it('keeps the caret in place when formatting leaves its text untouched', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: '# Heading\n\n\n\none two\nthree four\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    // Caret sits inside the heading, which the reflow does not move.
    view!.dispatch({ selection: { anchor: 3, head: 3 } })
    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    exposed.formatNow()

    expect(view!.state.selection.main.head).toBe(3)
  })

  it('clamps the caret to the end when the document shrinks past it', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: '# Heading\n\n\n\none two\nthree four\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    view!.dispatch({ selection: { anchor: view!.state.doc.length, head: view!.state.doc.length } })
    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    exposed.formatNow()

    expect(view!.state.selection.main.head).toBe(view!.state.doc.length)
  })

  it('never rewrites text to the left of the caret on the active line', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: 'aaa   \n\n\n\nactive line here\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    const offset = 'aaa   \n\n\n\nactive'.length
    view!.dispatch({ selection: { anchor: offset, head: offset } })
    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    exposed.formatNow()

    const head = view!.state.selection.main.head
    const line = view!.state.doc.lineAt(head)
    expect(view!.state.doc.sliceString(line.from, head)).toBe('active')
  })

  it('preserves the selected text exactly across formatting', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: 'aaa   \n\n\n\nbbb ccc ddd\n\n\n\neee   \n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    const doc = view!.state.doc.toString()
    const anchor = doc.indexOf('bbb')
    const head = doc.indexOf('ddd') + 'ddd'.length
    view!.dispatch({ selection: { anchor, head } })
    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    exposed.formatNow()

    const sel = view!.state.selection.main
    expect(view!.state.doc.sliceString(sel.from, sel.to)).toBe('bbb ccc ddd')
  })

  it('maintains the caret viewport-relative position by scrolling exactly', async () => {
    wrapper = mount(IntentMarkdownEditor, {
      props: { modelValue: 'aaa   \n\n\n\nbbb ccc\n', committedContent: '' },
      attachTo: document.body,
    })
    await flushPromises()

    const view = getView(wrapper)
    expect(view).not.toBeNull()

    const offset = 'aaa   \n\n\n\nbbb'.length
    view!.dispatch({ selection: { anchor: offset, head: offset } })

    // Simulate the caret rendering 30px higher after the reflow; the scroll
    // position must move by the same delta so it stays put in the viewport.
    let calls = 0
    ;(view as unknown as { coordsAtPos: (pos: number) => { top: number } }).coordsAtPos = () => {
      calls += 1
      return { top: calls === 1 ? 100 : 70 }
    }
    view!.scrollDOM.scrollTop = 0

    const exposed = wrapper.vm as unknown as { formatNow: () => string }
    exposed.formatNow()

    expect(view!.scrollDOM.scrollTop).toBe(-30)
  })
})