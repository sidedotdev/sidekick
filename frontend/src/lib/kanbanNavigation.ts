import type { InjectionKey } from 'vue'
import type { AgentType, FullTask } from './models'

export const KANBAN_COLUMN_AGENT_TYPES: readonly AgentType[] = ['human', 'llm', 'none']

const FINISHED_COLUMN = KANBAN_COLUMN_AGENT_TYPES.indexOf('none')

/**
 * Matches the wrapper of each rendered board card, which carries the task id
 * as data-task-id. Wrappers are focusable only programmatically, so cards stay
 * out of the tab order. The focused card is the keyboard highlight.
 */
export const BOARD_CARD_SELECTOR = '[data-board-card]'

/** Matches the board's other navigable controls, identified by their data-board-nav value. */
export const BOARD_CONTROL_SELECTOR = '[data-board-nav]'

export const boardControlIds = {
  headingAddTask: (agentType: AgentType) => `heading-add-task:${agentType}`,
  groupToggle: (groupKey: string) => `group-toggle:${groupKey}`,
  groupArchive: (groupKey: string) => `group-archive:${groupKey}`,
  groupAddTask: (groupKey: string, agentType: AgentType) => `group-add-task:${groupKey}:${agentType}`,
}

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

/** A header row control, sitting above the given column. */
export interface BoardHeaderControl {
  id: string
  column: number
}

/**
 * A navigable board row, top to bottom: a controls row holds at most one
 * control per column, a header row spans all columns, and a cards row holds
 * one expanded group's card columns.
 */
export type BoardRow =
  | { kind: 'controls'; controls: (string | null)[] }
  | { kind: 'header'; controls: BoardHeaderControl[] }
  | { kind: 'cards'; group: BoardGroup }

/**
 * A position within the board rows. On a header row, column is the column
 * vertical moves continue in, and index picks the control. On a controls row
 * index is always 0.
 */
export interface BoardCursor {
  row: number
  column: number
  index: number
}

export type BoardItem = { kind: 'card'; task: FullTask } | { kind: 'control'; id: string }

/** The result of a move: a new cursor, leaving the board past its left or top edge, or nothing. */
export type BoardMove = BoardCursor | { exit: 'left' | 'up' } | null

export interface BoardGroupLayout {
  key: string
  tasks: FullTask[]
  /** How the group's header row renders, or null when it has none */
  header: { archivable: boolean } | null
  expanded: boolean
}

const columnControls = (idFor: (agentType: AgentType) => string): (string | null)[] =>
  KANBAN_COLUMN_AGENT_TYPES.map(agentType => agentType === 'none' ? null : idFor(agentType))

/**
 * Lays out the board as rendered: the column-heading add buttons, then per
 * group its header, and when expanded its cards and add-task buttons.
 */
export const buildBoardRows = (groups: BoardGroupLayout[]): BoardRow[] => {
  const rows: BoardRow[] = [{ kind: 'controls', controls: columnControls(boardControlIds.headingAddTask) }]
  for (const group of groups) {
    if (group.header) {
      const controls: BoardHeaderControl[] = [{ id: boardControlIds.groupToggle(group.key), column: 0 }]
      if (group.header.archivable) {
        controls.push({ id: boardControlIds.groupArchive(group.key), column: FINISHED_COLUMN })
      }
      rows.push({ kind: 'header', controls })
    }
    if (!group.expanded) continue
    rows.push({ kind: 'cards', group: buildBoardGroup(group.key, group.tasks) })
    rows.push({ kind: 'controls', controls: columnControls(agentType => boardControlIds.groupAddTask(group.key, agentType)) })
  }
  return rows
}

/** The card groups of the expanded groups, in display order. */
export const visibleBoardGroups = (rows: BoardRow[]): BoardGroup[] =>
  rows.flatMap(row => row.kind === 'cards' ? [row.group] : [])

export const boardItemAt = (rows: BoardRow[], cursor: BoardCursor): BoardItem | null => {
  const row = rows[cursor.row]
  switch (row?.kind) {
    case 'controls': {
      const id = row.controls[cursor.column]
      return id ? { kind: 'control', id } : null
    }
    case 'header': {
      const control = row.controls[cursor.index]
      return control ? { kind: 'control', id: control.id } : null
    }
    case 'cards': {
      const task = row.group.columns[cursor.column]?.[cursor.index]
      return task ? { kind: 'card', task } : null
    }
    default:
      return null
  }
}

export const findCardCursor = (rows: BoardRow[], taskId: string): BoardCursor | null => {
  for (let row = 0; row < rows.length; row++) {
    const current = rows[row]
    if (current.kind !== 'cards') continue
    const position = findTaskPosition([current.group], taskId)
    if (position) return { row, column: position.column, index: position.index }
  }
  return null
}

/**
 * Locates a control. On a header row, the cursor continues in headerColumn
 * when given (the column the user arrived from), else the control's own.
 */
export const findControlCursor = (rows: BoardRow[], controlId: string, headerColumn?: number): BoardCursor | null => {
  for (let row = 0; row < rows.length; row++) {
    const current = rows[row]
    if (current.kind === 'controls') {
      const column = current.controls.indexOf(controlId)
      if (column >= 0) return { row, column, index: 0 }
    } else if (current.kind === 'header') {
      const index = current.controls.findIndex(control => control.id === controlId)
      if (index >= 0) return { row, column: headerColumn ?? current.controls[index].column, index }
    }
  }
  return null
}

const rowHasItemInColumn = (row: BoardRow, column: number): boolean => {
  switch (row.kind) {
    case 'controls':
      return row.controls[column] != null
    case 'header':
      return row.controls.length > 0
    case 'cards':
      return (row.group.columns[column]?.length ?? 0) > 0
  }
}

/** The first navigable item in display order. */
export const firstBoardCursor = (rows: BoardRow[]): BoardCursor | null => {
  for (let row = 0; row < rows.length; row++) {
    const current = rows[row]
    const columnCount = current.kind === 'controls' ? current.controls.length : KANBAN_COLUMN_AGENT_TYPES.length
    for (let column = 0; column < columnCount; column++) {
      if (rowHasItemInColumn(current, column)) return { row, column, index: 0 }
    }
  }
  return null
}

/**
 * Vertical moves keep the column, stepping through a column's cards and then
 * to the nearest row with an item in that column (header rows span all
 * columns). Horizontal moves go to the nearest item in the same row, keeping
 * the card index where possible. Past the left edge of any row, or the top of
 * the first row, the move exits the board.
 */
export const moveBoardCursor = (rows: BoardRow[], cursor: BoardCursor, direction: BoardDirection): BoardMove => {
  const current = rows[cursor.row]
  if (!current) return null
  const step = direction === 'up' || direction === 'left' ? -1 : 1

  if (direction === 'up' || direction === 'down') {
    if (current.kind === 'cards') {
      const index = cursor.index + step
      if (index >= 0 && index < (current.group.columns[cursor.column]?.length ?? 0)) {
        return { ...cursor, index }
      }
    }
    for (let row = cursor.row + step; row >= 0 && row < rows.length; row += step) {
      const next = rows[row]
      if (!rowHasItemInColumn(next, cursor.column)) continue
      const index = next.kind === 'cards' && step < 0 ? next.group.columns[cursor.column].length - 1 : 0
      return { row, column: cursor.column, index }
    }
    return direction === 'up' && cursor.row === 0 ? { exit: 'up' } : null
  }

  const exit: BoardMove = direction === 'left' ? { exit: 'left' } : null
  switch (current.kind) {
    case 'header': {
      const control = current.controls[cursor.index + step]
      return control ? { row: cursor.row, column: control.column, index: cursor.index + step } : exit
    }
    case 'controls': {
      for (let column = cursor.column + step; column >= 0 && column < current.controls.length; column += step) {
        if (current.controls[column] != null) return { row: cursor.row, column, index: 0 }
      }
      return exit
    }
    case 'cards': {
      const columns = current.group.columns
      for (let column = cursor.column + step; column >= 0 && column < columns.length; column += step) {
        if (columns[column].length > 0) {
          return { row: cursor.row, column, index: Math.min(cursor.index, columns[column].length - 1) }
        }
      }
      return exit
    }
  }
}

export interface CardFocusRequest {
  taskId: string
  /** Focus the card even when a non-card element holds focus */
  force: boolean
  /** False once a later focus move superseded this request */
  isCurrent(): boolean
}

/** A rendered list of board cards, able to reveal (and focus) the ones it holds. */
export interface BoardCardList {
  hasCard(taskId: string): boolean
  /** Returns whether this list holds the requested task. */
  revealCard(request: CardFocusRequest): boolean
}

export interface BoardCardRegistry {
  /** Returns a function unregistering the list. */
  register(list: BoardCardList): () => void
}

/** Provided by the board so its (virtualized) card lists can register themselves. */
export const BOARD_CARD_LISTS_KEY: InjectionKey<BoardCardRegistry> = Symbol('boardCardLists')
