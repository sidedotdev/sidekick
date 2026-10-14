import { describe, expect, it } from 'vitest'
import { buildSubflowTrees } from './subflow'
import { SubflowStatus, type FlowAction, type Subflow } from './models'

const action = (id: string, subflowId?: string): FlowAction => ({
  id, subflowId, subflow: 'wrong:|:legacy',
  created: new Date(`2026-01-01T00:00:0${id}.000Z`),
  updated: new Date(), flowId: 'flow', workspaceId: 'ws',
  actionType: 'run_command', actionParams: {}, actionResult: '',
  actionStatus: 'complete', isHumanAction: false,
})

const subflow = (id: string, parentSubflowId?: string): Subflow => ({
  id, parentSubflowId, name: 'Repeated name', description: id,
  workspaceId: 'ws', flowId: 'flow', status: SubflowStatus.Complete,
})

describe('paginated subflow trees', () => {
  it('uses real ancestry and IDs, merging interleaved actions with duplicate names', () => {
    const parent = subflow('parent')
    const left = subflow('left', parent.id)
    const right = subflow('right', parent.id)
    const actions = [action('3', 'left'), action('2', 'right'), action('1', 'left')]
    const trees = buildSubflowTrees(actions, { parent, left, right })
    expect(trees).toEqual([{
      id: 'parent', name: parent.name, description: 'parent',
      children: [
        { id: 'left', name: left.name, description: 'left', children: [actions[2], actions[0]] },
        { id: 'right', name: right.name, description: 'right', children: [actions[1]] },
      ],
    }])
  })

  it('retains missing-ancestor actions and replaces placeholders when data arrives', () => {
    const child = subflow('child', 'parent')
    const item = action('1', child.id)
    const partial = buildSubflowTrees([item], { child })
    expect(partial[0].id).toBe('parent')
    expect(partial[0].children[0]).toMatchObject({ id: child.id, children: [item] })
    const complete = buildSubflowTrees([item], { child, parent: subflow('parent') })
    expect(complete[0]).toMatchObject({ id: 'parent', name: 'Repeated name' })
  })

  it('ignores legacy names and retains ungrouped actions', () => {
    const item = action('1')
    const trees = buildSubflowTrees([item], {})
    expect(trees).toHaveLength(1)
    expect(trees[0].name).toBe('Actions')
    expect(trees[0].children).toEqual([item])
  })

  it('renders subflows without loaded actions and tolerates cycles', () => {
    const trees = buildSubflowTrees([], {
      a: subflow('a', 'b'),
      b: subflow('b', 'a'),
      empty: subflow('empty'),
    })
    const ids: string[] = []
    const walk = (nodes: typeof trees) => nodes.forEach(node => {
      ids.push(node.id!)
      walk(node.children.filter(child => !('actionType' in child)) as typeof trees)
    })
    walk(trees)
    expect(ids.sort()).toEqual(['a', 'b', 'empty'])
  })
})