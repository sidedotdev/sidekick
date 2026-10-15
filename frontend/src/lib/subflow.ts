import type { FlowAction, Subflow, SubflowTree } from "./models";

const getFlowActionTimestamp = (value: FlowAction['created'] | FlowAction['updated'] | undefined): number | null => {
  if (!value) {
    return null
  }

  const timestamp = new Date(value).getTime()
  return Number.isNaN(timestamp) ? null : timestamp
}

export const buildSubflowTrees = (
  flowActions: FlowAction[],
  subflowsById: Record<string, Subflow>,
): SubflowTree[] => {
  const roots: SubflowTree[] = []
  const nodes = new Map<string, SubflowTree>()
  const parents = new Map<string, string>()
  const firstActionTime = new Map<SubflowTree | FlowAction, number>()

  const ensureNode = (id: string): SubflowTree => {
    const existing = nodes.get(id)
    if (existing) return existing
    const subflow = subflowsById[id]
    const node: SubflowTree = {
      id,
      name: subflow?.name ?? 'Loading subflow…',
      description: subflow?.description,
      children: [],
    }
    nodes.set(id, node)
    if (subflow?.parentSubflowId) ensureNode(subflow.parentSubflowId)
    return node
  }

  Object.keys(subflowsById).sort().forEach(ensureNode)
  for (const action of flowActions) {
    if (action.subflowId) ensureNode(action.subflowId)
  }
  for (const [id, node] of nodes) {
    const parentId = subflowsById[id]?.parentSubflowId
    let ancestor = parentId
    const visited = new Set([id])
    while (ancestor && !visited.has(ancestor)) {
      visited.add(ancestor)
      ancestor = parents.get(ancestor)
    }
    if (parentId && !ancestor) {
      parents.set(id, parentId)
      nodes.get(parentId)!.children.push(node)
    } else {
      roots.push(node)
    }
  }

  const ungrouped: SubflowTree = { id: 'ungrouped-actions', name: 'Actions', children: [] }
  for (const action of flowActions) {
    const parent = action.subflowId ? nodes.get(action.subflowId)! : ungrouped
    parent.children.push(action)
    firstActionTime.set(action, getFlowActionTimestamp(action.created) ?? 0)
  }
  if (ungrouped.children.length) roots.push(ungrouped)

  const order = (node: SubflowTree): number => {
    for (const child of node.children) {
      if (!('actionType' in child)) firstActionTime.set(child, order(child))
    }
    node.children = node.children.filter(child => 'actionType' in child || child.children.length > 0)
    node.children.sort((a, b) =>
      (firstActionTime.get(a)! - firstActionTime.get(b)!) ||
      (a.id ?? '').localeCompare(b.id ?? ''),
    )
    const time = node.children.length ? firstActionTime.get(node.children[0])! : Infinity
    firstActionTime.set(node, time)
    return time
  }
  roots.forEach(order)
  roots.sort((a, b) => (firstActionTime.get(a)! - firstActionTime.get(b)!) || a.id!.localeCompare(b.id!))
  return roots.filter(node => node.children.length > 0)
}

export function containsFlowAction(tree: SubflowTree, actionId: string | undefined): boolean {
  if (!actionId) return false
  return tree.children.some(child => 'actionType' in child
    ? child.id === actionId
    : containsFlowAction(child, actionId))
}