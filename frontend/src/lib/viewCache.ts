import type { FullTask, Flow, FlowAction, Subflow, SubflowTree, Workspace } from './models'

export interface FlowViewCacheEntry {
  flow: Flow
  flowActions: FlowAction[]
  subflowsById: Record<string, Subflow>
  subflowTrees: SubflowTree[]
  workspace: Workspace | null
}

const MAX_CACHED_FLOWS = 10

class ViewCache {
  private kanbanTasks = new Map<string, FullTask[]>()
  private flowViews = new Map<string, FlowViewCacheEntry>()

  getKanbanTasks(workspaceId: string): FullTask[] | null {
    return this.kanbanTasks.get(workspaceId) ?? null
  }

  setKanbanTasks(workspaceId: string, tasks: FullTask[]): void {
    this.kanbanTasks.set(workspaceId, [...tasks])
  }

  getFlowView(flowId: string): FlowViewCacheEntry | null {
    return this.flowViews.get(flowId) ?? null
  }

  setFlowView(flowId: string, entry: FlowViewCacheEntry): void {
    this.flowViews.delete(flowId)
    this.flowViews.set(flowId, {
      flow: entry.flow,
      flowActions: [...entry.flowActions],
      subflowsById: { ...entry.subflowsById },
      subflowTrees: [...entry.subflowTrees],
      workspace: entry.workspace,
    })

    while (this.flowViews.size > MAX_CACHED_FLOWS) {
      const oldestKey = this.flowViews.keys().next().value
      if (oldestKey) this.flowViews.delete(oldestKey)
    }
  }
}

export const viewCache = new ViewCache()