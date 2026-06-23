<template>
  <div ref="editorParent" class="intent-markdown-editor"></div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { keymap } from '@codemirror/view'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { markdown, markdownLanguage } from '@codemirror/lang-markdown'
import {
  HighlightStyle,
  codeFolding,
  ensureSyntaxTree,
  foldEffect,
  syntaxHighlighting,
} from '@codemirror/language'
import { tags as t } from '@lezer/highlight'
import { yamlFrontmatter } from '@codemirror/lang-yaml'
import { applyUncommittedHighlight, uncommittedHighlightExtension } from '../lib/intent_diff_editor'

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
  const selectionBg = isDark ? 'rgba(131, 58, 180, 0.65)' : 'rgba(131, 58, 180, 0.45)'
  return EditorView.theme(
    {
      '&': { backgroundColor: 'transparent', color: 'var(--color-text)', height: '100%' },
      '.cm-scroller': { fontFamily: '"JetBrains Mono", monospace', overflow: 'auto' },
      '.cm-content': { padding: '1.75rem 2rem', lineHeight: '1.6' },
      '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: 'var(--color-text-2)' },
      // CodeMirror sizes each gutter element to match its corresponding editor
      // line's height. Markdown headings render with a larger font-size and
      // therefore taller line boxes; centring keeps line numbers visually
      // aligned with the (also vertically-centred) heading text and caret.
      '.cm-gutterElement': { display: 'flex', alignItems: 'center', justifyContent: 'flex-end' },
      '.cm-lineNumbers .cm-gutterElement': { paddingRight: '0.5em' },
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
      '.cm-intent-uncommitted': {
        backgroundColor: isDark ? 'rgba(25, 197, 24, 0.10)' : 'rgba(25, 197, 24, 0.28)',
        borderRadius: '4px',
        padding: '0 0.5em',
        margin: '0 -0.5em',
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

// foldFrontmatter collapses the YAML frontmatter block so it gets out of the
// way of the actual intent content. The range covers the content between the
// two `---` DashLine nodes so the delimiter lines remain visible as a single
// folded indicator.
const foldFrontmatter = () => {
  if (!editorView) return
  const state = editorView.state
  const tree = ensureSyntaxTree(state, state.doc.length, 50)
  if (!tree) return
  const frontmatter = tree.topNode.getChild('Frontmatter')
  if (!frontmatter) return
  const content = frontmatter.getChild('FrontmatterContent')
  if (!content || content.to <= content.from) return
  editorView.dispatch({
    effects: foldEffect.of({ from: content.from, to: content.to }),
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
              { key: 'Mod-Enter', preventDefault: true, run: submitShortcut },
            ]),
          ),
          basicSetup,
          yamlFrontmatter({ content: markdown({ base: markdownLanguage }) }),
          codeFolding(),
          syntaxHighlighting(markdownHighlightStyle),
          EditorView.lineWrapping,
          keymap.of([
            {
              key: 'Tab',
              preventDefault: true,
              run: (view) => {
                view.dispatch(
                  view.state.update(view.state.replaceSelection(tabIndent()), {
                    scrollIntoView: true,
                    userEvent: 'input.indent',
                  }),
                )
                return true
              },
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
})
</script>

<style scoped>
.intent-markdown-editor {
  height: 100%;
  width: 100%;
}
</style>