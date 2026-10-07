import type { AgentType, FullTask } from './models'

export const KANBAN_COLUMN_AGENT_TYPES: readonly AgentType[] = ['human', 'llm', 'none']

/**
 * Matches the wrapper of each rendered board card, which carries the task id
 * as data-task-id. Only the highlighted card's wrapper is focusable, and only
 * programmatically, so cards stay out of the tab order.
 */
export const BOARD_CARD_SELECTOR = '[data-board-card]'

/**
 * Groups tasks into kanban columns, each ordered most recently updated first.
 * Columns without tasks are absent.
 */
export const groupTasksByAgentType = (tasks: FullTask[]): Record<AgentType, FullTask[]> => {
  const grouped = tasks.reduce((acc, task) => {
    if (!acc[task.agentType]) {
      acc[task.agentType] = []
    }
    acc[task.agentType].push(task)
    return acc
  }, {} as Record<AgentType, FullTask[]>)

  for (const columnTasks of Object.values(grouped)) {
    columnTasks.sort((a: FullTask, b: FullTask) => {
      if (b.updated === a.updated) {
        return b.id > a.id ? 1 : -1
      }
      return b.updated > a.updated ? 1 : -1
    })
  }

  return grouped
}

/** A displayed board group, with its columns in left-to-right order. */
export interface BoardGroup {
  key: string
  columns: FullTask[][]
}

export interface BoardPosition {
  group: number
  column: number
  index: number
}

export type BoardDirection = 'up' | 'down' | 'left' | 'right'

export const buildBoardGroup = (key: string, tasks: FullTask[]): BoardGroup => {
  const grouped = groupTasksByAgentType(tasks)
  return { key, columns: KANBAN_COLUMN_AGENT_TYPES.map(agentType => grouped[agentType] ?? []) }
}

export const taskAtPosition = (groups: BoardGroup[], position: BoardPosition): FullTask | undefined =>
  groups[position.group]?.columns[position.column]?.[position.index]

export const findTaskPosition = (groups: BoardGroup[], taskId: string): BoardPosition | null => {
  for (let group = 0; group < groups.length; group++) {
    const columns = groups[group].columns
    for (let column = 0; column < columns.length; column++) {
      const index = columns[column].findIndex(task => task.id === taskId)
      if (index >= 0) return { group, column, index }
    }
  }
  return null
}

/** The top-left card: first card of the first non-empty column in the topmost non-empty group. */
export const defaultBoardPosition = (groups: BoardGroup[]): BoardPosition | null => {
  for (let group = 0; group < groups.length; group++) {
    const column = groups[group].columns.findIndex(tasks => tasks.length > 0)
    if (column >= 0) return { group, column, index: 0 }
  }
  return null
}

/**
 * Moves within a column, continuing into the same column of adjacent groups
 * vertically, or to the nearest non-empty column horizontally. Returns null
 * when there is nothing in that direction.
 */
export const moveBoardPosition = (
  groups: BoardGroup[],
  position: BoardPosition,
  direction: BoardDirection,
): BoardPosition | null => {
  const { group, column, index } = position
  switch (direction) {
    case 'up': {
      if (index > 0) return { group, column, index: index - 1 }
      for (let g = group - 1; g >= 0; g--) {
        const length = groups[g].columns[column]?.length ?? 0
        if (length > 0) return { group: g, column, index: length - 1 }
      }
      return null
    }
    case 'down': {
      const length = groups[group]?.columns[column]?.length ?? 0
      if (index + 1 < length) return { group, column, index: index + 1 }
      for (let g = group + 1; g < groups.length; g++) {
        if ((groups[g].columns[column]?.length ?? 0) > 0) return { group: g, column, index: 0 }
      }
      return null
    }
    case 'left':
    case 'right': {
      const columns = groups[group]?.columns ?? []
      const step = direction === 'left' ? -1 : 1
      for (let c = column + step; c >= 0 && c < columns.length; c += step) {
        if (columns[c].length > 0) {
          return { group, column: c, index: Math.min(index, columns[c].length - 1) }
        }
      }
      return null
    }
  }
}