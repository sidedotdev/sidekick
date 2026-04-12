import { describe, it, expect, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import KanbanBoard from '../KanbanBoard.vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import type { FullTask } from '../../lib/models'

describe('KanbanBoard', () => {
  const defaultProps = {
    workspaceId: 'test-workspace-id',
    tasks: [] as FullTask[],
    showGuidedOverlay: false
  }

  it('renders no tasks when tasks prop is empty', () => {
    const wrapper = shallowMount(KanbanBoard, { props: defaultProps })
    const lists = wrapper.findAllComponents(VirtualTaskList)
    lists.forEach(list => {
      expect(list.props().tasks.length).toBe(0)
    })
  })

  it('renders tasks in the correct columns', () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'to_do'       },
      { id: '2', agentType: 'llm'  , status: 'in_progress' },
      { id: '3', agentType: 'none' , status: 'complete'    },
      { id: '4', agentType: 'llm'  , status: 'to_do'       },
    ] as FullTask[]
    const wrapper = shallowMount(KanbanBoard, { props: { ...defaultProps, tasks } })
    const columns = wrapper.findAll('.kanban-column')
    expect(columns.length).toBe(3)
    expect(columns.at(0)!.findComponent(VirtualTaskList).props().tasks.length).toBe(1) // human
    expect(columns.at(1)!.findComponent(VirtualTaskList).props().tasks.length).toBe(2) // llm
    expect(columns.at(2)!.findComponent(VirtualTaskList).props().tasks.length).toBe(1) // none
  })

  it('displays tasks in descending order of id', () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'drafting' },
      { id: '2', agentType: 'human', status: 'drafting' },
      { id: '3', agentType: 'human', status: 'drafting' },
    ] as FullTask[]

    const wrapper = shallowMount(KanbanBoard, {
      props: { ...defaultProps, tasks, showGuidedOverlay: false }
    })

    const columns = wrapper.findAll('.kanban-column')
    const humanTasks = columns.at(0)!.findComponent(VirtualTaskList).props().tasks
    expect(humanTasks[0].id).toBe('3')
    expect(humanTasks[1].id).toBe('2')
    expect(humanTasks[2].id).toBe('1')
  })
})