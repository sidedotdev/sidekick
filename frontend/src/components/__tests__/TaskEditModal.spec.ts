import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TaskEditModal from '../TaskEditModal.vue'
import type { Task } from '../../lib/models'

describe('TaskEditModal', () => {
  let wrapper: ReturnType<typeof mount>
  const task: Task = {
    id: 'task_1',
    workspaceId: 'ws_1',
    title: 'Test Task',
    description: 'This is a test task',
    status: 'to_do',
    agentType: 'llm',
    flows: [{
      workspaceId: 'test1',
      id: 'flow1',
      status: 'in_progress',
      parentId: 'task_1',
      type: "basic_dev",
    }],
  } as unknown as Task

  beforeEach(() => {
    localStorage.clear()
    wrapper = mount(TaskEditModal, {
      props: { task },
    })
  })

  it('renders without errors', () => {
    expect(wrapper.exists()).toBe(true)
  })

  it('displays the task edit fields in the form fields', () => {
    // TODO we don't have title yet // expect((wrapper.find('input[type="text"]').element as HTMLInputElement).value).toBe(task.title);
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe(task.description);
    expect((wrapper.find('select#status').element as HTMLSelectElement).value).toBe(task.status);
    expect((wrapper.find('select#flowType').element as HTMLSelectElement).value).toBe(task.flows[0].type);
  })

  it('makes a PUT request to the correct endpoint with the correct request body when the form is submitted', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = mockFetch

    await wrapper.find('form').trigger('submit.prevent')

    expect(mockFetch).toHaveBeenCalledWith(`/api/v1/workspaces/${task.workspaceId}/tasks/${task.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: task.description,
        flowType: task.flows[0].type,
        status: task.status,
        flowOptions: {
          planningPrompt: '',
          determineRequirements: true,
        },
      })
    })
    expect(wrapper.emitted().updated).toBeTruthy()
  })

  it('includes edited task description/status/flowtype in request body when form is submitted', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = mockFetch

    const textarea = wrapper.find('textarea')
    await textarea.setValue(task.description + " - edited")
    const flowTypeEl = wrapper.find('#flowType')
    await flowTypeEl.setValue('planned_dev')
    const statusEl = wrapper.find('select#status')
    await statusEl.setValue('drafting')
    await wrapper.find('form').trigger('submit.prevent')

    expect(mockFetch).toHaveBeenCalledWith(`/api/v1/workspaces/${task.workspaceId}/tasks/${task.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: task.description + " - edited",
        flowType: 'planned_dev',
        status: 'drafting',
        flowOptions: {
          planningPrompt: '',
          determineRequirements: true,
        },
      }),
    })
    expect(wrapper.emitted().updated).toBeTruthy()
  })

})