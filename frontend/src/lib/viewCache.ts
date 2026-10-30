import type { FullTask, Flow, Project, Workspace } from './models'

export interface FlowViewCacheEntry {
  flow: Flow
  workspace: Workspace | null
}

const MAX_CACHED_FLOWS = 10

class ViewCache {
  private kanbanTasks = new Map<string, FullTask[]>()
  private projects = new Map<string, Project[]>()
  private flowViews = new Map<string, FlowViewCacheEntry>()

  getKanbanTasks(workspaceId: string): FullTask[] | null {
    return this.kanbanTasks.get(workspaceId) ?? null
  }

  setKanbanTasks(workspaceId: string, tasks: FullTask[]): void {
    this.kanbanTasks.set(workspaceId, [...tasks])
  }

  getProjects(workspaceId: string): Project[] | null {
    return this.projects.get(workspaceId) ?? null
  }

  setProjects(workspaceId: string, projects: Project[]): void {
    this.projects.set(workspaceId, [...projects])
  }

  getFlowView(flowId: string): FlowViewCacheEntry | null {
    return this.flowViews.get(flowId) ?? null
  }

  setFlowView(flowId: string, entry: FlowViewCacheEntry): void {
    this.flowViews.delete(flowId)
    this.flowViews.set(flowId, {
      flow: entry.flow,
      workspace: entry.workspace,
    })

    while (this.flowViews.size > MAX_CACHED_FLOWS) {
      const oldestKey = this.flowViews.keys().next().value
      if (oldestKey) this.flowViews.delete(oldestKey)
    }
  }
}

export const viewCache = new ViewCache()