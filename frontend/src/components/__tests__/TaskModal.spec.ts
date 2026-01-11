import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { config, mount, VueWrapper, flushPromises } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import TaskModal from '../TaskModal.vue'
import type { Task } from '../../lib/models'
import { nextTick } from 'process'
import { wrap } from 'module'
import { h } from 'vue'

config.global.plugins.push(PrimeVue)

const mockStore = vi.hoisted(() => ({
  workspaceId: 'test-workspace-id',
  getBranchCache: () => null,
  setBranchCache: () => {},
}))

vi.mock('../../lib/store', () => ({
  store: mockStore
}))

const mockBranchesResponse = {
  branches: ['main', 'feature-1', 'feature-2']
}

const createMockFetch = () => {
  return vi.fn((url: string, _options?: RequestInit) => {
    if (url.includes('/branches')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(mockBranchesResponse),
      })
    }
    if (url.includes('/tasks')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ id: 'new-task-id' }),
      })
    }
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({}),
    })
  })
}

const createTestTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'existing-task-id',
  workspaceId: 'test-workspace-123',
  description: 'Existing task description',
  status: 'to_do',
  agentType: 'llm',
  flowType: 'basic_dev',
  ...overrides,
})

const DropdownStub = {
  name: 'Dropdown',
  props: ['modelValue', 'options', 'optionLabel', 'optionValue'],
  emits: ['update:modelValue', 'change'],
  template: `<select :value="modelValue" @change="$emit('change', { value: $event.target.value }); $emit('update:modelValue', $event.target.value)">
    <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
  </select>`
}

describe('TaskModal', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    vi.spyOn(window, 'alert').mockImplementation(() => {})
  })

  const mountComponent = (props = {}) => {
    wrapper = mount(TaskModal, {
      props,
      global: {
        stubs: {
          Dropdown: DropdownStub
        }
      }
    })
  }

  it('renders without errors', () => {
    mountComponent()
    expect(wrapper.exists()).toBe(true)
  })

  it('renders with generic Task header', () => {
    mountComponent()
    expect(wrapper.find('h2').text()).toBe('Task')
  })

  it('renders with same Task header when editing', () => {
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
    expect(wrapper.find('h2').text()).toBe('Task')
  })

  it('has a close button in the header', () => {
    mountComponent()
    const closeButton = wrapper.find('.close-button')
    expect(closeButton.exists()).toBe(true)
  })

  it('emits close event when close button is clicked', async () => {
    mountComponent()
    await wrapper.find('.close-button').trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('renders segmented control for flow type and workdir', () => {
    mountComponent()
    expect(wrapper.findAllComponents({ name: 'SegmentedControl' })).toHaveLength(2)
  })

  it('submits form with correct API call for create', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    mountComponent()
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('Test description')
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

  it('emits created event when a new task is successfully created', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock

    mountComponent()
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('Test description')
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
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('Test description')
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

  it('closes modal without confirmation when overlay is clicked', async () => {
    mountComponent()
    await wrapper.find('.overlay').trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('has Start Task button as primary action', async () => {
    mountComponent()
    const startButton = wrapper.find('.p-button-primary')
    expect(startButton.text()).toBe('Start Task')
  })

  it('submits with to_do status when Start Task is clicked', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock
    
    mountComponent()
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('Test description')
    
    await wrapper.find('.p-button-primary').trigger('click')
    await flushPromises()

    const requestBody = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(requestBody.status).toBe('to_do')
  })
})

describe('TaskModal localStorage behavior', () => {
  const testWorkspaceId = 'test-workspace-123'

  beforeEach(() => {
    vi.stubGlobal('fetch', createMockFetch())
    localStorage.clear()
    mockStore.workspaceId = testWorkspaceId
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  describe('auto-save behavior', () => {
    it('auto-saves draft after debounce when description changes', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'new-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      await textarea.setValue('New description')
      await wrapper.vm.$nextTick()
      
      // Fast-forward past debounce timer
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      expect(fetchMock).toHaveBeenCalledWith(
        `/api/v1/workspaces/${testWorkspaceId}/tasks`,
        expect.objectContaining({
          method: 'POST',
          body: expect.stringContaining('"status":"drafting"')
        })
      )
      vi.useRealTimers()
    })

    it('does not auto-save when description is empty', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ id: 'new-task-id' }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      // Set empty description
      await textarea.setValue('')
      await wrapper.vm.$nextTick()
      
      // Fast-forward past debounce timer
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      expect(fetchMock).not.toHaveBeenCalled()
      vi.useRealTimers()
    })

    it('does not auto-save when description is only whitespace', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ id: 'new-task-id' }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      // Set whitespace-only description
      await textarea.setValue('   ')
      await wrapper.vm.$nextTick()
      
      // Fast-forward past debounce timer
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      expect(fetchMock).not.toHaveBeenCalled()
      vi.useRealTimers()
    })

    it('uses PUT for subsequent auto-saves after task is created', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'created-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      // First change triggers POST
      await textarea.setValue('First description')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      // Second change should trigger PUT
      await textarea.setValue('Updated description')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      expect(fetchMock).toHaveBeenLastCalledWith(
        `/api/v1/workspaces/${testWorkspaceId}/tasks/created-task-id`,
        expect.objectContaining({
          method: 'PUT'
        })
      )
      vi.useRealTimers()
    })

    it('does not load draft description from localStorage for new task', () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'Old localStorage draft')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      expect(textarea.element.value).toBe('')
    })

    it('loads description from task prop when editing existing task', () => {
      const wrapper = mount(TaskModal, {
        props: {
          task: createTestTask()
        }
      })

      const textarea = wrapper.find('textarea')
      expect(textarea.element.value).toBe('Existing task description')
    })
  })

  describe('branch selection persistence', () => {
    it('loads last selected branch from localStorage for new task', () => {
      const branchKey = `lastSelectedBranch_${testWorkspaceId}`
      localStorage.setItem(branchKey, 'feature-1')

      const wrapper = mount(TaskModal)

      expect((wrapper.vm as any).selectedBranch).toBe('feature-1')
    })

    it('saves selected branch to localStorage after successful task creation', async () => {
      const branchKey = `lastSelectedBranch_${testWorkspaceId}`

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      await textarea.setValue('Task with branch')

      ;(wrapper.vm as any).envType = 'local_git_worktree'
      ;(wrapper.vm as any).selectedBranch = 'feature-2'
      await wrapper.vm.$nextTick()

      const form = wrapper.find('form')
      await form.trigger('submit')
      await flushPromises()

      expect(localStorage.getItem(branchKey)).toBe('feature-2')
    })

    it('does not load branch from localStorage when editing existing task', () => {
      const branchKey = `lastSelectedBranch_${testWorkspaceId}`
      localStorage.setItem(branchKey, 'should-not-appear')

      const wrapper = mount(TaskModal, {
        props: {
          task: createTestTask({
            flowOptions: {
              startBranch: 'existing-branch'
            }
          })
        }
      })

      expect((wrapper.vm as any).selectedBranch).toBe('existing-branch')
    })

    it('uses workspace-specific key for branch selection', () => {
      const otherWorkspaceId = 'other-workspace-456'
      const otherBranchKey = `lastSelectedBranch_${otherWorkspaceId}`
      localStorage.setItem(otherBranchKey, 'other-branch')

      const wrapper = mount(TaskModal)

      expect((wrapper.vm as any).selectedBranch).toBeNull()
    })

    it('does not save branch when null after task creation', async () => {
      const branchKey = `lastSelectedBranch_${testWorkspaceId}`

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      await textarea.setValue('Task without branch')

      const form = wrapper.find('form')
      await form.trigger('submit')
      await flushPromises()

      expect(localStorage.getItem(branchKey)).toBeNull()
    })
  })

  describe('close behavior', () => {
    it('saves pending changes when closing with dirty state', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'new-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      // Make a change but don't wait for debounce
      await textarea.setValue('Unsaved description')
      await wrapper.vm.$nextTick()
      
      // Close immediately (before debounce fires)
      await wrapper.find('.close-button').trigger('click')
      await flushPromises()
      
      // Should have saved the pending changes
      expect(fetchMock).toHaveBeenCalledWith(
        `/api/v1/workspaces/${testWorkspaceId}/tasks`,
        expect.objectContaining({
          method: 'POST',
          body: expect.stringContaining('Unsaved description')
        })
      )
      expect(wrapper.emitted('close')).toBeTruthy()
      vi.useRealTimers()
    })

    it('does not save when closing with empty description', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ ok: true })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      
      // Close without entering anything
      await wrapper.find('.close-button').trigger('click')
      await flushPromises()
      
      expect(fetchMock).not.toHaveBeenCalled()
      expect(wrapper.emitted('close')).toBeTruthy()
      vi.useRealTimers()
    })
  })

  describe('save indicator', () => {
    it('shows saving indicator when dirty', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'new-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      await textarea.setValue('Some description')
      await wrapper.vm.$nextTick()
      
      // Should show saving indicator immediately (during debounce)
      const indicator = wrapper.find('.save-indicator')
      expect(indicator.classes()).toContain('saving')
      expect(indicator.text()).toBe('Saving...')
      
      vi.useRealTimers()
    })

    it('shows saved indicator after successful save', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'new-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      await textarea.setValue('Some description')
      await wrapper.vm.$nextTick()
      
      // Wait for debounce and save
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      const indicator = wrapper.find('.save-indicator')
      expect(indicator.classes()).toContain('saved')
      expect(indicator.text()).toBe('Saved')
      
      vi.useRealTimers()
    })

    it('hides saved indicator after 3 seconds', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn().mockResolvedValue({ 
        ok: true, 
        json: () => Promise.resolve({ task: { id: 'new-task-id' } }) 
      })
      global.fetch = fetchMock

      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      await textarea.setValue('Some description')
      await wrapper.vm.$nextTick()
      
      // Wait for debounce and save
      vi.advanceTimersByTime(1600)
      await flushPromises()
      
      // Wait for saved indicator to hide
      vi.advanceTimersByTime(3100)
      await wrapper.vm.$nextTick()
      
      const indicator = wrapper.find('.save-indicator')
      expect(indicator.classes()).toContain('idle')
      
      vi.useRealTimers()
    })
  })

  describe('undo/redo functionality', () => {
    it('supports undo with Ctrl+Z', async () => {
      vi.useFakeTimers()
      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      await textarea.setValue('First value')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(100)
      
      await textarea.setValue('Second value')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(100)
      
      // Trigger undo
      await wrapper.find('.modal').trigger('keydown', { key: 'z', ctrlKey: true })
      await wrapper.vm.$nextTick()
      
      expect((wrapper.vm as any).description).toBe('First value')
      vi.useRealTimers()
    })

    it('supports redo with Ctrl+Y', async () => {
      vi.useFakeTimers()
      const wrapper = mount(TaskModal)
      const textarea = wrapper.find('textarea')
      
      await textarea.setValue('First value')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(100)
      
      await textarea.setValue('Second value')
      await wrapper.vm.$nextTick()
      vi.advanceTimersByTime(100)
      
      // Undo then redo
      await wrapper.find('.modal').trigger('keydown', { key: 'z', ctrlKey: true })
      await wrapper.vm.$nextTick()
      await wrapper.find('.modal').trigger('keydown', { key: 'y', ctrlKey: true })
      await wrapper.vm.$nextTick()
      
      expect((wrapper.vm as any).description).toBe('Second value')
      vi.useRealTimers()
    })
  })
})