<template>
  <div ref="editorParent" class="intent-markdown-editor"></div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { keymap, scrollPastEnd } from '@codemirror/view'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { markdown, markdownLanguage } from '@codemirror/lang-markdown'
import {
  HighlightStyle,
  codeFolding,
  ensureSyntaxTree,
  foldEffect,
  indentUnit,
  syntaxHighlighting,
} from '@codemirror/language'
import { indentLess, indentMore } from '@codemirror/commands'
import { tags as t } from '@lezer/highlight'
import { yamlFrontmatter } from '@codemirror/lang-yaml'
import { applyUncommittedHighlight, uncommittedHighlightExtension } from '../lib/intent_diff_editor'
import { formatMarkdownPreservingSelection } from '../lib/markdown_format'

const props = withDefaults(
  defineProps<{
    modelValue: string
    committedContent?: string
    tabSize?: number
  }>(),
  { committedContent: '', tabSize: 2 },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'shortcut-submit'): void
}>()

const editorParent = ref<HTMLElement | null>(null)
let editorView: EditorView | null = null
let applyingExternal = false
const FORMAT_WRAP_COLUMN = 80

const themeCompartment = new Compartment()
const darkModeMedia =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null

const tabIndent = () => ' '.repeat(Math.max(1, props.tabSize))

const markdownHighlightStyle = HighlightStyle.define([
  { tag: t.heading1, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.6em' },
  { tag: t.heading2, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.35em' },
  { tag: t.heading3, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.2em' },
  { tag: t.heading4, color: 'var(--color-heading)', fontWeight: '700', fontSize: '1.1em' },
  { tag: [t.heading5, t.heading6], color: 'var(--color-heading)', fontWeight: '700' },
  { tag: t.strong, color: 'var(--color-heading)', fontWeight: '700' },
  { tag: t.emphasis, fontStyle: 'italic' },
  { tag: t.strikethrough, textDecoration: 'line-through' },
  { tag: t.link, color: 'var(--color-link)' },
  { tag: t.url, color: 'var(--color-link)', textDecoration: 'underline' },
  { tag: t.monospace, fontFamily: '"JetBrains Mono", monospace', color: 'var(--color-text-2)' },
  { tag: t.list, color: 'var(--color-text)' },
  { tag: t.quote, color: 'var(--color-text-2)', fontStyle: 'italic' },
  { tag: t.meta, color: 'var(--color-text-2)' },
  { tag: t.processingInstruction, color: 'var(--color-text-2)' },
  { tag: t.contentSeparator, color: 'var(--color-text-2)' },
  { tag: t.atom, color: 'var(--color-primary)' },
  { tag: t.bool, color: 'var(--color-primary)' },
  { tag: t.number, color: 'var(--color-primary)' },
  { tag: t.string, color: 'var(--color-green-dark)' },
  { tag: t.keyword, color: 'var(--color-link)', fontWeight: '600' },
  { tag: t.propertyName, color: 'var(--color-link)' },
])

const buildEditorTheme = (isDark: boolean) => {
  // A very transparent tint of the primary brand color keeps text legible
  // while still standing out against the active-line background.
  const selectionBg = `color-mix(in srgb, var(--color-primary) ${isDark ? 35 : 25}%, transparent)`
  return EditorView.theme(
    {
      '&': { backgroundColor: 'transparent', color: 'var(--color-text)', height: '100%' },
      // overscrollBehavior 'none' stops the rubber-band/bounce animation when
      // scrolling fast past the content bounds; the scroller simply halts at
      // its edges instead of overshooting and springing back.
      '.cm-scroller': {
        fontFamily: '"JetBrains Mono", monospace',
        overflow: 'auto',
        overscrollBehavior: 'none',
      },
      '.cm-content': { padding: '1.75rem 2rem', lineHeight: '1.6' },
      '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: 'var(--color-text-2)' },
      // CodeMirror sizes each gutter element to match its corresponding editor
      // line's height. Markdown headings render with a larger font-size and
      // therefore taller line boxes; centring keeps line numbers visually
      // aligned with the (also vertically-centred) heading text and caret.
      '.cm-gutterElement': { display: 'flex', alignItems: 'center', justifyContent: 'flex-end' },
      '.cm-lineNumbers .cm-gutterElement': { paddingRight: '0.5em' },
      // The default fold gutter ships separate glyphs for open ("⌄") and
      // closed ("›") states. Replace both with the closed glyph and rotate
      // it 90° when expanded so the indicator keeps a single shape that
      // simply spins about its centerpoint.
      '.cm-foldGutter .cm-gutterElement span': {
        display: 'inline-block',
        fontSize: '0',
        lineHeight: '1',
      },
      '.cm-foldGutter .cm-gutterElement span::before': {
        content: '"›"',
        display: 'inline-block',
        fontSize: '1rem',
        lineHeight: '1',
        transition: 'transform 0.1s',
      },
      '.cm-foldGutter .cm-gutterElement span[title="Fold line"]::before': {
        transform: 'rotate(90deg)',
      },
      '.cm-activeLine': { backgroundColor: 'var(--color-background-mute)' },
      '.cm-activeLineGutter': { backgroundColor: 'transparent' },
      '&.cm-focused': { outline: 'none' },
      // CodeMirror applies an inline `z-index: -2` to the selection layer so
      // that the host page's selection styles can sit beneath the content; we
      // need it above .cm-activeLine so partial selections stay visible.
      '.cm-selectionLayer': { zIndex: '2 !important' },
      '.cm-selectionBackground, ::selection': { backgroundColor: selectionBg },
      '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground': {
        backgroundColor: selectionBg,
      },
      '.cm-content ::selection': { backgroundColor: selectionBg },
      // Uncommitted spans are merged across adjacent same-line additions
      // (see intent_diff_editor.ts) so a flat background with no padding
      // is enough to read as a single contiguous highlight.
      '.cm-intent-uncommitted': {
        backgroundColor: isDark ? 'rgba(25, 197, 24, 0.18)' : 'rgba(25, 197, 24, 0.28)',
        borderRadius: '2px',
      },
    },
    { dark: isDark },
  )
}

const submitShortcut = () => {
  emit('shortcut-submit')
  return true
}

const refreshUncommittedHighlight = () => {
  if (!editorView) return
  applyUncommittedHighlight(editorView, props.committedContent)
}

// caretTop reports the caret's vertical screen offset, or null when layout
// measurement is unavailable (e.g. headless test environments, where
// coordsAtPos throws because the DOM exposes no client rects).
const caretTop = (view: EditorView, pos: number): number | null => {
  try {
    return view.coordsAtPos(pos)?.top ?? null
  } catch {
    return null
  }
}

// formatDocument reflows plain paragraphs and collapses extra blank lines,
// leaving frontmatter and fenced code blocks alone so executable/structured
// content is never silently rewritten. It runs as part of saving rather than
// while editing, and minimizes disruption: the block holding the caret is left
// verbatim so text to the left of the cursor never changes, the selection maps
// onto the same content after the reflow, and the caret is pinned to the same
// vertical position within the viewport.
const formatDocument = () => {
  if (!editorView) return
  const view = editorView
  const current = view.state.doc.toString()
  const { anchor, head } = view.state.selection.main
  const {
    text: formatted,
    anchor: newAnchor,
    head: newHead,
  } = formatMarkdownPreservingSelection(current, anchor, head, FORMAT_WRAP_COLUMN)
  if (formatted === current) return
  const beforeTop = caretTop(view, head)
  // Suppress the external-change emit so the host owns syncing the formatted
  // text back into its model, avoiding a redundant save round-trip.
  applyingExternal = true
  view.dispatch({
    changes: { from: 0, to: current.length, insert: formatted },
    selection: { anchor: newAnchor, head: newHead },
    userEvent: 'input.format',
  })
  applyingExternal = false
  refreshUncommittedHighlight()
  if (beforeTop === null) return
  const afterTop = caretTop(view, view.state.selection.main.head)
  if (afterTop === null) return
  view.scrollDOM.scrollTop += afterTop - beforeTop
}

// foldFrontmatter collapses the YAML frontmatter block so it gets out of the
// way of the actual intent content. The range covers everything between the
// opening and closing `---` DashLine nodes so the delimiter lines remain
// visible as a single folded indicator. The yaml-frontmatter parser exposes
// the inner YAML under a `Stream` node (the YAML grammar's root), not the
// `FrontmatterContent` placeholder declared by lang-yaml, so we locate the
// opening DashLine positionally instead of by node name. We fold from the end
// of the opening `---` line through the end of the whole frontmatter section
// so the entire block collapses onto the first line of the document.

// frontmatterParseTarget finds the position just past the frontmatter's
// closing `---` delimiter. Returns null when the document has no frontmatter
// delimiters at all.
const frontmatterParseTarget = (state: EditorState): number | null => {
  if (state.doc.line(1).text !== '---') return null
  for (let i = 2; i <= state.doc.lines; i++) {
    const line = state.doc.line(i)
    if (line.text === '---') return line.to
  }
  return null
}

const foldFrontmatter = () => {
  if (!editorView) return
  const state = editorView.state
  // Only parse up to the end of the frontmatter: ensureSyntaxTree's time
  // budget can be exceeded when parsing a whole large document on a slow or
  // heavily loaded machine, which would silently skip the fold.
  const parseTarget = frontmatterParseTarget(state)
  if (parseTarget === null) return
  const tree = ensureSyntaxTree(state, parseTarget, 50)
  if (!tree) return
  const frontmatter = tree.topNode.getChild('Frontmatter')
  if (!frontmatter) return
  const openDash = frontmatter.getChild('DashLine')
  if (!openDash) return
  const from = openDash.to
  const to = frontmatter.to
  if (to <= from) return
  editorView.dispatch({
    effects: foldEffect.of({ from, to }),
  })
}

const setEditorDoc = (text: string) => {
  if (!editorView) return
  applyingExternal = true
  editorView.dispatch({
    changes: { from: 0, to: editorView.state.doc.length, insert: text },
  })
  applyingExternal = false
}

const createEditor = () => {
  if (editorView || !editorParent.value) return
  try {
    editorView = new EditorView({
      parent: editorParent.value,
      state: EditorState.create({
        doc: props.modelValue,
        extensions: [
          Prec.highest(
            keymap.of([
              { key: 'Mod-i', preventDefault: true, run: submitShortcut },
            ]),
          ),
          basicSetup,
          yamlFrontmatter({ content: markdown({ base: markdownLanguage }) }),
          codeFolding(),
          syntaxHighlighting(markdownHighlightStyle),
          EditorView.lineWrapping,
          // Allow scrolling until the last line reaches the top of the editor,
          // rather than stopping once the document's end is merely visible.
          scrollPastEnd(),
          indentUnit.of(tabIndent()),
          keymap.of([
            {
              key: 'Tab',
              preventDefault: true,
              run: (view) => {
                if (view.state.selection.ranges.some((range) => !range.empty)) {
                  return indentMore(view)
                }
                view.dispatch(
                  view.state.update(view.state.replaceSelection(tabIndent()), {
                    scrollIntoView: true,
                    userEvent: 'input.indent',
                  }),
                )
                return true
              },
            },
            {
              key: 'Shift-Tab',
              preventDefault: true,
              run: indentLess,
            },
          ]),
          uncommittedHighlightExtension(),
          EditorView.updateListener.of((update) => {
            if (update.docChanged && !applyingExternal) {
              emit('update:modelValue', update.state.doc.toString())
              refreshUncommittedHighlight()
            }
          }),
          themeCompartment.of(buildEditorTheme(darkModeMedia?.matches ?? false)),
        ],
      }),
    })
  } catch (e) {
    console.error('Failed to initialize editor:', e)
  }
}

const handleColorSchemeChange = (event: MediaQueryListEvent) => {
  if (!editorView) return
  editorView.dispatch({
    effects: themeCompartment.reconfigure(buildEditorTheme(event.matches)),
  })
}

// Replace the document and re-apply highlight/folding when the parent swaps
// the bound file's content (e.g. on file open). Edits typed inside the editor
// flow back through `update:modelValue`, so the guard avoids the resulting
// echo from triggering a full document replacement.
watch(
  () => props.modelValue,
  async (next) => {
    if (!editorView) {
      await nextTick()
      if (!editorView) createEditor()
    }
    if (!editorView) return
    if (editorView.state.doc.toString() === next) return
    setEditorDoc(next)
    refreshUncommittedHighlight()
    foldFrontmatter()
  },
)

watch(() => props.committedContent, refreshUncommittedHighlight)

onMounted(async () => {
  await nextTick()
  createEditor()
  refreshUncommittedHighlight()
  foldFrontmatter()
  darkModeMedia?.addEventListener('change', handleColorSchemeChange)
})

onBeforeUnmount(() => {
  darkModeMedia?.removeEventListener('change', handleColorSchemeChange)
  editorView?.destroy()
  editorView = null
})

defineExpose({
  focus: () => editorView?.focus(),
  // formatNow reflows the current document immediately, returning the resulting text.
  // Auto-saving runs this so persisted intent is always formatted;
  // the selection and the caret's vertical viewport position are preserved across the reflow to keep editing undisturbed.
  // Also used when launching an intent sub-task so the committed intent state is formatted before being saved, matching the idle-format behavior.
  formatNow: (): string => {
    formatDocument()
    return editorView?.state.doc.toString() ?? ''
  },
  // Test-only handle. Returns the underlying CodeMirror EditorView so specs
  // can inspect parser/fold state without dragging in a real browser. Not
  // part of the component's public API and should not be used by app code.
  __editorViewForTest: () => editorView,
})
</script>

<style scoped>
.intent-markdown-editor {
  height: 100%;
  width: 100%;
}
</style>
