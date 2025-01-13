import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import TaskModal from '../TaskModal.vue'
import type { Task } from '../../lib/models'

vi.mock('../../lib/store', () => ({
  store: {
    workspaceId: 'test-workspace-id'
  }
}))

describe('TaskModal', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
  })

  const mountComponent = (props = {}) => {
    wrapper = mount(TaskModal, { props })
  }

  it('renders without errors', () => {
    mountComponent()
    expect(wrapper.exists()).toBe(true)
  })

  it('renders in create mode when no task prop is provided', () => {
    mountComponent()
    expect(wrapper.find('h2').text()).toBe('New Task')
  })

  it('renders in edit mode when task prop is provided', () => {
    const task: Task = {
      id: '1',
      workspaceId: 'test-workspace-id',
      description: 'Test task',
      status: 'to_do',
      agentType: 'human',
      flowType: 'basic_dev',
      flowOptions: { determineRequirements: true, envType: 'local' }
    }
    mountComponent({ task })
    expect(wrapper.find('h2').text()).toBe('Edit Task')
  })

  it('renders segmented controls for status and flow type', () => {
    mountComponent()
    expect(wrapper.findAllComponents({ name: 'SegmentedControl' })).toHaveLength(2)
  })

  it('submits form with correct API call for create', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    mountComponent()
    await wrapper.find('form').trigger('submit')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/test-workspace-id/tasks',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: expect.any(String)
      })
    )
  })

  it('submits form with correct API call for edit', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    const task: Task = {
      id: '1',
      workspaceId: 'test-workspace-id',
      description: 'Test task',
      status: 'to_do',
      agentType: 'human',
      flowType: 'basic_dev',
      flowOptions: { determineRequirements: true, envType: 'local' }
    }
    mountComponent({ task })
    await wrapper.find('form').trigger('submit')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/test-workspace-id/tasks/1',
      expect.objectContaining({
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: expect.any(String)
      })
    )
  })

  it('populates form fields correctly in edit mode', async () => {
    const task: Task = {
      id: '1',
      workspaceId: 'test-workspace-id',
      description: 'Test task',
      status: 'to_do',
      agentType: 'human',
      flowType: 'basic_dev',
      flowOptions: { determineRequirements: true, envType: 'local' }
    }
    mountComponent({ task })

    await wrapper.vm.$nextTick()

    await wrapper.vm.$nextTick()
    const descriptionElement = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea').element as HTMLTextAreaElement
    expect(descriptionElement.value).toBe('Test task')
    
    const checkboxElement = wrapper.find('input[type="checkbox"]').element as HTMLInputElement
    expect(checkboxElement.checked).toBe(true)
  })

  it('emits close event when close button is clicked', async () => {
    mountComponent()
    await wrapper.find('.cancel').trigger('click')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('emits created event when a new task is successfully created', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    mountComponent()
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('created')).toBeTruthy()
  })

  it('emits updated event when an existing task is successfully updated', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    const task: Task = {
      id: '1',
      workspaceId: 'test-workspace-id',
      description: 'Test task',
      status: 'to_do',
      agentType: 'human',
      flowType: 'basic_dev',
      flowOptions: { determineRequirements: true, envType: 'local' }
    }
    mountComponent({ task })
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('updated')).toBeTruthy()
  })

  it('updates localStorage with last used flow type and env type after form submission', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    mountComponent()
    const flowTypeControl = wrapper.findAllComponents({ name: 'SegmentedControl' }).find(c => c.props('options').some((opt: any) => opt.value === 'planned_dev'))
    await flowTypeControl?.vm.$emit('update:modelValue', 'planned_dev')
    await wrapper.vm.$nextTick()
    await wrapper.find('form').trigger('submit')

    expect(localStorage.getItem('lastUsedFlowType')).toBe('planned_dev')
    expect(localStorage.getItem('lastUsedEnvType')).toBe('local')
  })

  it('toggles determine requirements checkbox', async () => {
    mountComponent()
    const checkbox = wrapper.find('input[type="checkbox"]')
    await checkbox.setValue(false)
    expect((checkbox.element as HTMLInputElement).checked).toBe(false)
  })

  it('renders Workdir segmented control in devMode', async () => {
    const originalEnv = import.meta.env.MODE
    import.meta.env.MODE = 'development'

    mountComponent()
    expect(wrapper.findAllComponents({ name: 'SegmentedControl' })).toHaveLength(3)

    import.meta.env.MODE = originalEnv
  })

  it('calls safeClose with confirmation when there are unsaved changes', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockImplementation(() => true)
    mountComponent()
    
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('New description')
    await wrapper.vm.$nextTick()
    await wrapper.find('.overlay').trigger('click')

    expect(confirmSpy).toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeTruthy()

    confirmSpy.mockRestore()
  })

  it('does not close when confirmation is canceled', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockImplementation(() => false)
    mountComponent()
    
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('New description')
    await wrapper.vm.$nextTick()
    await wrapper.find('.overlay').trigger('click')

    expect(confirmSpy).toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeFalsy()

    confirmSpy.mockRestore()
  })
})