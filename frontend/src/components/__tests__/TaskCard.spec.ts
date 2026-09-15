import { describe, it, expect, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import TaskCard from '../TaskCard.vue'
import type { FullTask, Task } from '../../lib/models'

const { routerPushMock } = vi.hoisted(() => ({ routerPushMock: vi.fn() }))
vi.mock('@/router', () => ({ default: { push: routerPushMock } }))

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

  it('wraps the card in a task-card-shell element', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    const shell = wrapper.find('.task-card-shell')
    expect(shell.exists()).toBe(true)
    expect(shell.find('.task-card').exists()).toBe(true)
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

  it('emits an edit event with the task when the edit button is clicked', async () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    await wrapper.find('.action.edit').trigger('click')
    expect(wrapper.emitted('edit')).toEqual([[task]])
  })

  it('does not offer the full edit modal for a running task', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, status: 'in_progress' } },
    })
    expect(wrapper.find('.action.edit').exists()).toBe(false)
    expect(wrapper.find('.action.rename').exists()).toBe(true)
  })

  it('renames a running task with a title-only request', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    global.fetch = mockFetch

    const runningTask: FullTask = { ...task, status: 'in_progress' }
    const wrapper = shallowMount(TaskCard, {
      props: { task: runningTask },
      attachTo: document.body,
    })

    await wrapper.find('.action.rename').trigger('click')
    const input = wrapper.get('.task-title-input')
    expect((input.element as HTMLInputElement).value).toBe(runningTask.title)

    await input.setValue('Renamed Task')
    await input.trigger('keydown.enter')

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/ws_1/tasks/task_1', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Renamed Task' }),
    })
    expect(wrapper.emitted('updated')).toEqual([['task_1']])
    expect(wrapper.find('.task-title-input').exists()).toBe(false)
  })

  it('emits an error when the rename request fails', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('offline'))

    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, status: 'in_progress' } },
      attachTo: document.body,
    })

    await wrapper.find('.action.rename').trigger('click')
    const input = wrapper.get('.task-title-input')
    await input.setValue('Renamed Task')
    await input.trigger('keydown.enter')
    await flushPromises()

    expect(wrapper.emitted('updated')).toBeUndefined()
    expect(wrapper.emitted('error')).toEqual([['Failed to rename task']])
  })

  it('does not send a rename request when the edit is canceled', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    global.fetch = mockFetch

    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, status: 'in_progress' } },
      attachTo: document.body,
    })

    await wrapper.find('.action.rename').trigger('click')
    const input = wrapper.get('.task-title-input')
    await input.setValue('Renamed Task')
    await input.trigger('keydown.esc')

    expect(mockFetch).not.toHaveBeenCalled()
    expect(wrapper.find('.task-title-input').exists()).toBe(false)
  })

  it('emits a copy event with a duplicated task when the copy button is clicked', async () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task },
    })
    await wrapper.find('.action.copy').trigger('click')
    const emitted = wrapper.emitted('copy')
    expect(emitted).toHaveLength(1)
    const copied = emitted![0][0] as Task
    expect(copied.id).toBeUndefined()
    expect(copied.title).toBe(task.title)
    expect(copied.agentType).toBe('llm')
  })

  it('routes an idd task to the intent canvas using the idd flow, not a sub-task flow', async () => {
    routerPushMock.mockClear()
    const iddTask: FullTask = {
      ...task,
      flowType: 'idd',
      flows: [
        { workspaceId: 'ws_1', id: 'flow_subtask', type: 'basic_dev', parentId: 'task_1', status: 'in_progress' },
        { workspaceId: 'ws_1', id: 'flow_idd', type: 'idd', parentId: 'task_1', status: 'in_progress' },
      ],
    }
    const wrapper = shallowMount(TaskCard, {
      props: { task: iddTask },
    })
    await wrapper.find('.task-card').trigger('click')
    expect(routerPushMock).toHaveBeenCalledWith({ name: 'intent-canvas', params: { id: 'flow_idd' } })
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

  it.each([
    ['local', { envType: 'local' }],
    ['local_git_worktree', { envType: 'local_git_worktree' }],
    ['undefined envType', { envType: undefined }],
  ])('renders no env indicator for %s', (_name, flowOptions) => {
    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, flowOptions } },
    })
    expect(wrapper.find('.env-indicator').exists()).toBe(false)
  })

  it('renders no env indicator when flowOptions is null', () => {
    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, flowOptions: null } },
    })
    expect(wrapper.find('.env-indicator').exists()).toBe(false)
  })

  it.each([
    ['devpod', 'DevPod container'],
    ['openshell', 'OpenShell container'],
    ['modal', 'Modal cloud sandbox'],
  ])('renders an env indicator with tooltip for %s', (envType, title) => {
    const wrapper = shallowMount(TaskCard, {
      props: { task: { ...task, flowOptions: { envType } } },
    })
    expect(wrapper.get('.env-indicator').attributes('title')).toBe(title)
  })
})