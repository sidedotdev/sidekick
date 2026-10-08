import { describe, expect, it } from 'vitest'
import {
  boardControlIds,
  boardItemAt,
  buildBoardGroup,
  buildBoardRows,
  defaultBoardPosition,
  findCardCursor,
  findControlCursor,
  firstBoardCursor,
  moveBoardCursor,
  taskAtPosition,
  visibleBoardGroups,
  type BoardDirection,
} from '../kanbanNavigation'
import type { AgentType, FullTask } from '../models'

const task = (id: string, agentType: AgentType, updated: string): FullTask =>
  ({ id, agentType, status: 'to_do', updated } as unknown as FullTask)

// Group a: human [a1, a2], llm [], none [a3] (archivable)
// Group b: human [b1], llm [b2, b3], none []
// Group c: collapsed
const rows = buildBoardRows([
  {
    key: 'a',
    header: { archivable: true },
    expanded: true,
    tasks: [task('a2', 'human', '2024-01-01'), task('a1', 'human', '2024-01-02'), task('a3', 'none', '2024-01-01')],
  },
  {
    key: 'b',
    header: { archivable: false },
    expanded: true,
    tasks: [task('b1', 'human', '2024-01-01'), task('b3', 'llm', '2024-01-01'), task('b2', 'llm', '2024-01-02')],
  },
  { key: 'c', header: { archivable: false }, expanded: false, tasks: [task('c1', 'human', '2024-01-01')] },
])

const ids = boardControlIds

const itemId = (cursor: Parameters<typeof boardItemAt>[1]): string | null => {
  const item = boardItemAt(rows, cursor)
  if (!item) return null
  return item.kind === 'card' ? item.task.id : item.id
}

const move = (fromId: string, direction: BoardDirection, headerColumn?: number): string | null => {
  const from = findCardCursor(rows, fromId) ?? findControlCursor(rows, fromId, headerColumn)
  expect(from, fromId).not.toBeNull()
  const next = moveBoardCursor(rows, from!, direction)
  if (next === null) return null
  if ('exit' in next) return `exit-${next.exit}`
  return itemId(next)
}

describe('kanbanNavigation', () => {
  it('defaults to the first card of the first non-empty column of the topmost group', () => {
    const groups = visibleBoardGroups(rows)
    expect(taskAtPosition(groups, defaultBoardPosition(groups)!)!.id).toBe('a1')
    const llmOnly = [buildBoardGroup('empty', []), buildBoardGroup('x', [task('x1', 'llm', '2024-01-01')])]
    expect(taskAtPosition(llmOnly, defaultBoardPosition(llmOnly)!)!.id).toBe('x1')
    expect(defaultBoardPosition([buildBoardGroup('empty', [])])).toBeNull()
  })

  it('lays out collapsed groups as just their header row', () => {
    expect(visibleBoardGroups(rows).map(group => group.key)).toEqual(['a', 'b'])
    expect(findCardCursor(rows, 'c1')).toBeNull()
    expect(findControlCursor(rows, ids.groupToggle('c'))).not.toBeNull()
    expect(findControlCursor(rows, ids.groupAddTask('c', 'human'))).toBeNull()
    expect(findControlCursor(rows, ids.groupArchive('b'))).toBeNull()
  })

  it('lays out a headerless group without a header row', () => {
    const bare = buildBoardRows([{ key: '', header: null, expanded: true, tasks: [task('x1', 'llm', '2024-01-01')] }])
    expect(bare.map(row => row.kind)).toEqual(['controls', 'cards', 'controls'])
    expect(itemId(firstBoardCursor(bare)!)).toBe(ids.headingAddTask('human'))
  })

  it.each<[string, BoardDirection, string | null]>([
    ['a1', 'down', 'a2'],
    ['a2', 'down', ids.groupAddTask('a', 'human')],
    [ids.groupAddTask('a', 'human'), 'down', ids.groupToggle('b')],
    [ids.groupToggle('b'), 'down', 'b1'],
    ['b1', 'down', ids.groupAddTask('b', 'human')],
    [ids.groupAddTask('b', 'human'), 'down', ids.groupToggle('c')],
    [ids.groupToggle('c'), 'down', null],
    ['a3', 'down', ids.groupToggle('b')],
    ['a1', 'up', ids.groupToggle('a')],
    ['a3', 'up', ids.groupToggle('a')],
    ['b2', 'up', ids.groupToggle('b')],
    ['b3', 'up', 'b2'],
    [ids.groupAddTask('a', 'llm'), 'up', ids.groupToggle('a')],
    [ids.groupToggle('a'), 'up', ids.headingAddTask('human')],
    [ids.headingAddTask('human'), 'down', ids.groupToggle('a')],
    [ids.headingAddTask('llm'), 'up', 'exit-up'],
    [ids.headingAddTask('human'), 'left', 'exit-left'],
    [ids.headingAddTask('human'), 'right', ids.headingAddTask('llm')],
    [ids.headingAddTask('llm'), 'right', null],
    [ids.groupToggle('a'), 'right', ids.groupArchive('a')],
    [ids.groupArchive('a'), 'right', null],
    [ids.groupArchive('a'), 'left', ids.groupToggle('a')],
    [ids.groupToggle('a'), 'left', 'exit-left'],
    [ids.groupArchive('a'), 'down', 'a3'],
    [ids.groupAddTask('a', 'llm'), 'left', ids.groupAddTask('a', 'human')],
    [ids.groupAddTask('a', 'llm'), 'right', null],
    ['a1', 'right', 'a3'],
    ['a3', 'left', 'a1'],
    ['a3', 'right', null],
    ['a1', 'left', 'exit-left'],
    ['b1', 'right', 'b2'],
    ['b3', 'left', 'b1'],
    ['b3', 'right', null],
  ])('moves from %s %s to %s', (from, direction, expected) => {
    expect(move(from, direction)).toBe(expected)
  })

  it('continues vertically from a header row in the column the user arrived from', () => {
    expect(move(ids.groupToggle('b'), 'down', 1)).toBe('b2')
    expect(move(ids.groupToggle('b'), 'down', 2)).toBe(ids.groupToggle('c'))
    expect(move(ids.groupToggle('a'), 'up', 2)).toBeNull()
  })
})