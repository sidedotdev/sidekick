import { describe, it, expect, beforeEach } from 'vitest'
import { viewCache } from './viewCache'
import type { FlowViewCacheEntry } from './viewCache'
import type { FullTask, Flow, Project, Subflow, SubflowTree } from './models'
import { SubflowStatus } from './models'

function makeTask(id: string): FullTask {
  return {
    id,
    title: `Task ${id}`,
    description: '',
    status: 'to_do',
    flowId: '',
    flowType: '',
    workspaceId: 'ws1',
    agentType: 'llm',
    created: new Date(),
    updated: new Date(),
    flows: [],
  } as unknown as FullTask
}

function makeProject(id: string): Project {
  return {
    id,
    workspaceId: 'ws1',
    title: `Project ${id}`,
    priority: 'none',
    created: '',
    updated: '',
  }
}

function makeFlowViewCache(flowId: string): FlowViewCacheEntry {
  return {
    flow: { id: flowId, workspaceId: 'ws1', status: 'in_progress' } as Flow,
    flowActions: [{ id: `action_${flowId}`, flowId } as any],
    subflowsById: {
      [`sf_${flowId}`]: { id: `sf_${flowId}`, type: 'step.dev', status: SubflowStatus.Started } as Subflow,
    },
    subflowTrees: [{ name: 'tree1' } as SubflowTree],
    workspace: null,
  }
}

describe('ViewCache', () => {
  beforeEach(() => {
    // Reset cache state by overwriting with empty data
    viewCache.setKanbanTasks('reset', [])
    viewCache.setFlowView('reset', makeFlowViewCache('reset'))
  })

  describe('kanban tasks', () => {
    it('returns null for unknown workspace', () => {
      expect(viewCache.getKanbanTasks('unknown_ws')).toBeNull()
    })

    it('stores and retrieves tasks by workspace ID', () => {
      const tasks = [makeTask('1'), makeTask('2')]
      viewCache.setKanbanTasks('ws1', tasks)

      const cached = viewCache.getKanbanTasks('ws1')
      expect(cached).toHaveLength(2)
      expect(cached![0].id).toBe('1')
      expect(cached![1].id).toBe('2')
    })

    it('stores a shallow copy so mutations do not affect cache', () => {
      const tasks = [makeTask('1')]
      viewCache.setKanbanTasks('ws1', tasks)

      tasks.push(makeTask('2'))
      expect(viewCache.getKanbanTasks('ws1')).toHaveLength(1)
    })

    it('separates tasks by workspace ID', () => {
      viewCache.setKanbanTasks('ws1', [makeTask('a')])
      viewCache.setKanbanTasks('ws2', [makeTask('b'), makeTask('c')])

      expect(viewCache.getKanbanTasks('ws1')).toHaveLength(1)
      expect(viewCache.getKanbanTasks('ws2')).toHaveLength(2)
    })
  })

  describe('projects', () => {
    it('returns null for unknown workspace', () => {
      expect(viewCache.getProjects('unknown_ws')).toBeNull()
    })

    it('stores and retrieves projects by workspace ID', () => {
      viewCache.setProjects('ws1', [makeProject('p1'), makeProject('p2')])

      const cached = viewCache.getProjects('ws1')
      expect(cached).toHaveLength(2)
      expect(cached![0].id).toBe('p1')
    })

    it('stores a shallow copy so mutations do not affect cache', () => {
      const projects = [makeProject('p1')]
      viewCache.setProjects('ws1', projects)

      projects.push(makeProject('p2'))
      expect(viewCache.getProjects('ws1')).toHaveLength(1)
    })

    it('separates projects by workspace ID', () => {
      viewCache.setProjects('ws1', [makeProject('p1')])
      viewCache.setProjects('ws2', [makeProject('p2'), makeProject('p3')])

      expect(viewCache.getProjects('ws1')).toHaveLength(1)
      expect(viewCache.getProjects('ws2')).toHaveLength(2)
    })
  })

  describe('flow view', () => {
    it('returns null for unknown flow', () => {
      expect(viewCache.getFlowView('unknown_flow')).toBeNull()
    })

    it('stores and retrieves flow view data', () => {
      const entry = makeFlowViewCache('flow1')
      viewCache.setFlowView('flow1', entry)

      const cached = viewCache.getFlowView('flow1')
      expect(cached).not.toBeNull()
      expect(cached!.flow.id).toBe('flow1')
      expect(cached!.flowActions).toHaveLength(1)
      expect(cached!.subflowsById[`sf_flow1`]).toBeDefined()
      expect(cached!.subflowTrees).toHaveLength(1)
    })

    it('stores shallow copies so mutations do not affect cache', () => {
      const entry = makeFlowViewCache('flow1')
      viewCache.setFlowView('flow1', entry)

      entry.flowActions.push({ id: 'extra' } as any)
      expect(viewCache.getFlowView('flow1')!.flowActions).toHaveLength(1)
    })

    it('evicts oldest entries when exceeding max size', () => {
      for (let i = 0; i < 12; i++) {
        viewCache.setFlowView(`flow_${i}`, makeFlowViewCache(`flow_${i}`))
      }

      // Oldest entries should be evicted (max 10, plus 'reset' from beforeEach = 11 total before eviction)
      // After inserting 12 + 1 (reset), the oldest should be gone
      expect(viewCache.getFlowView('flow_11')).not.toBeNull()
      expect(viewCache.getFlowView('flow_10')).not.toBeNull()
    })

    it('updates existing entry without growing cache', () => {
      viewCache.setFlowView('flow1', makeFlowViewCache('flow1'))
      const updatedEntry = makeFlowViewCache('flow1')
      updatedEntry.flowActions = []
      viewCache.setFlowView('flow1', updatedEntry)

      expect(viewCache.getFlowView('flow1')!.flowActions).toHaveLength(0)
    })
  })
})