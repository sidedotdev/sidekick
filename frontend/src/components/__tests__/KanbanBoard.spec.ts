import { describe, it, expect, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import KanbanBoard from '../KanbanBoard.vue'
import TaskCard from '../TaskCard.vue'
import type { Task } from '../../lib/models'

describe('KanbanBoard', () => {
  it('renders no tasks when tasks prop is empty', () => {
    const wrapper = shallowMount(KanbanBoard, { props: { tasks: [] } })
    expect(wrapper.findAll('.task-card').length).toBe(0)
  })

  it('renders tasks in the correct columns', () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'to_do'       },
      { id: '2', agentType: 'llm'  , status: 'in_progress' },
      { id: '3', agentType: 'none' , status: 'complete'    },
      { id: '4', agentType: 'llm'  , status: 'to_do'       },
    ] as Task[]
    const wrapper = shallowMount(KanbanBoard, { props: { tasks } })
    const columns = wrapper.findAll('.kanban-column')
    expect(columns.length).toBe(3)
    expect(columns.at(0)!.findAllComponents(TaskCard).length).toBe(1) // human
    expect(columns.at(1)!.findAllComponents(TaskCard).length).toBe(2) // llm
    expect(columns.at(2)!.findAllComponents(TaskCard).length).toBe(1) // none
  })
  it('displays tasks in descending order of id', () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'drafting' },
      { id: '2', agentType: 'human', status: 'drafting' },
      { id: '3', agentType: 'human', status: 'drafting' },
    ] as Task[]

    const wrapper = shallowMount(KanbanBoard, {
      props: { tasks }
    })

    const taskCards = wrapper.findAllComponents(TaskCard)
    expect(taskCards[0].props().task.id).toBe('3')
    expect(taskCards[1].props().task.id).toBe('2')
    expect(taskCards[2].props().task.id).toBe('1')
  })
})