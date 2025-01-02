import { describe, it, expect, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import TaskCard from '../TaskCard.vue'
import TaskModal from '../TaskModal.vue'
import type { FullTask } from '../../lib/models'

describe('TaskCard', () => {
const task: FullTask = {
  id: 'task_1',
  workspaceId: 'ws_1',
  title: 'Test Task',
  description: 'This is a test task',
  status: 'drafting',
  agentType: 'llm',
  flowType: 'basic_dev',
  flows: [],
  created: new Date(),
  updated: new Date(),
}

  it('renders without errors', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('displays the task title, description, and status', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    expect(wrapper.text()).toContain(task.title)
    expect(wrapper.text()).toContain(task.description)
  })

  it('applies the correct status label class', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    const statusLabel = wrapper.get('.status-label')
    expect(statusLabel.classes()).toContain(task.status.toLowerCase())
  })

  it('renders the edit button', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    expect(wrapper.find('.action.edit').exists()).toBe(true)
  })

  it('opens the TaskModal when the edit button is clicked', async () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    await wrapper.find('.action.edit').trigger('click')
    expect(wrapper.findComponent(TaskModal).exists()).toBe(true)
  })

  it('calls the correct endpoint when delete button is clicked', async () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })

    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    global.fetch = mockFetch

    // Mock window.confirm to always return true
    window.confirm = () => true

    await wrapper.find('.action.delete').trigger('click')

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/ws_1/tasks/task_1', {
      method: 'DELETE',
    })
  })
})