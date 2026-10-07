import { describe, expect, it } from 'vitest'
import {
  buildBoardGroup,
  defaultBoardPosition,
  findTaskPosition,
  moveBoardPosition,
  taskAtPosition,
  type BoardDirection,
  type BoardGroup,
} from '../kanbanNavigation'
import type { AgentType, FullTask } from '../models'

const task = (id: string, agentType: AgentType, updated: string): FullTask =>
  ({ id, agentType, status: 'to_do', updated } as unknown as FullTask)

// Group A: human [a1, a2], llm [], none [a3]
// Group B: human [b1], llm [b2, b3], none []
const groups: BoardGroup[] = [
  buildBoardGroup('a', [
    task('a2', 'human', '2024-01-01'),
    task('a1', 'human', '2024-01-02'),
    task('a3', 'none', '2024-01-01'),
  ]),
  buildBoardGroup('b', [
    task('b1', 'human', '2024-01-01'),
    task('b3', 'llm', '2024-01-01'),
    task('b2', 'llm', '2024-01-02'),
  ]),
]

const move = (fromId: string, direction: BoardDirection): string | null => {
  const next = moveBoardPosition(groups, findTaskPosition(groups, fromId)!, direction)
  return next ? taskAtPosition(groups, next)!.id : null
}

describe('kanbanNavigation', () => {
  it('defaults to the first card of the first non-empty column of the topmost group', () => {
    expect(taskAtPosition(groups, defaultBoardPosition(groups)!)!.id).toBe('a1')
    const llmOnly = [buildBoardGroup('empty', []), buildBoardGroup('x', [task('x1', 'llm', '2024-01-01')])]
    expect(taskAtPosition(llmOnly, defaultBoardPosition(llmOnly)!)!.id).toBe('x1')
    expect(defaultBoardPosition([buildBoardGroup('empty', [])])).toBeNull()
  })

  it.each<[string, BoardDirection, string | null]>([
    ['a1', 'down', 'a2'],
    ['a2', 'down', 'b1'],
    ['b1', 'up', 'a2'],
    ['a1', 'up', null],
    ['b1', 'down', null],
    ['b2', 'up', null],
    ['a3', 'down', null],
    ['a1', 'right', 'a3'],
    ['a3', 'left', 'a1'],
    ['a3', 'right', null],
    ['a1', 'left', null],
    ['b1', 'right', 'b2'],
    ['b3', 'left', 'b1'],
    ['b3', 'right', null],
  ])('moves from %s %s to %s', (from, direction, expected) => {
    expect(move(from, direction)).toBe(expected)
  })
})