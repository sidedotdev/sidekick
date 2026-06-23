import { Decoration, EditorView } from '@codemirror/view'
import type { DecorationSet } from '@codemirror/view'
import { RangeSetBuilder, StateEffect, StateField } from '@codemirror/state'
import type { Extension } from '@codemirror/state'

import { diffAddedRanges } from './intent_diff'
import type { AddedRange } from './intent_diff'

const uncommittedMark = Decoration.mark({ class: 'cm-intent-uncommitted' })

const setUncommittedRanges = StateEffect.define<AddedRange[]>()

const uncommittedField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none
  },
  update(set, tr) {
    set = set.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setUncommittedRanges)) {
        const builder = new RangeSetBuilder<Decoration>()
        const sorted = effect.value
          .filter((r) => r.to > r.from)
          .slice()
          .sort((a, b) => a.from - b.from || a.to - b.to)
        for (const range of sorted) {
          builder.add(range.from, range.to, uncommittedMark)
        }
        set = builder.finish()
      }
    }
    return set
  },
  provide: (field) => EditorView.decorations.from(field),
})

// uncommittedHighlightExtension provides the editor extension that renders
// uncommitted intent ranges. Pair with `applyUncommittedHighlight` to update
// the highlighted ranges as the document or its committed baseline changes.
export const uncommittedHighlightExtension = (): Extension => uncommittedField

// mergeSameLineAdjacent collapses ranges whose gap on the document contains
// only intra-line whitespace, so that consecutive uncommitted words on the
// same line render as a single continuous highlight rather than several
// abutting spans separated by visible dividers.
const mergeSameLineAdjacent = (ranges: AddedRange[], text: string): AddedRange[] => {
  if (ranges.length <= 1) return ranges
  const sorted = ranges
    .filter((r) => r.to > r.from)
    .slice()
    .sort((a, b) => a.from - b.from || a.to - b.to)
  const merged: AddedRange[] = []
  for (const range of sorted) {
    const last = merged[merged.length - 1]
    if (last && range.from >= last.to) {
      const gap = text.slice(last.to, range.from)
      if (!gap.includes('\n') && /^\s*$/.test(gap)) {
        last.to = Math.max(last.to, range.to)
        continue
      }
    }
    merged.push({ from: range.from, to: range.to })
  }
  return merged
}

// applyUncommittedHighlight diffs `committed` against the editor's current
// content and dispatches an effect to highlight the resulting added ranges.
export const applyUncommittedHighlight = (view: EditorView, committed: string): void => {
  const current = view.state.doc.toString()
  const ranges = mergeSameLineAdjacent(diffAddedRanges(committed, current), current)
  view.dispatch({ effects: setUncommittedRanges.of(ranges) })
}