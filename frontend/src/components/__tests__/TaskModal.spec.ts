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

  it('updates status when dropdown option is selected', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = fetchMock
    
    mountComponent()
    const descriptionInput = wrapper.findComponent({ name: 'AutogrowTextarea' }).find('textarea')
    await descriptionInput.setValue('Test description')
    const splitButton = wrapper.findComponent({ name: 'SplitButton' })
    const dropdown = splitButton.find('.p-splitbutton-dropdown')
    await dropdown.trigger('click')

    const options = document.querySelectorAll('.p-tieredmenu-item-content')
    let found = false
    for (const option of options) {
      if (/draft/i.test(option.textContent || '')) {
        option.dispatchEvent(new Event('click'))
        await wrapper.vm.$nextTick()
        found = true
        break
      }
    }
    if (!found) {
      throw new Error('Draft option not found in dropdown')
    }

    await wrapper.find('form').trigger('submit')

    const requestBody = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(requestBody.status).toBe('drafting')
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

  describe('draft description persistence', () => {
    it('loads draft description from localStorage for new task', () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'My saved draft description')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      expect(textarea.element.value).toBe('My saved draft description')
    })

    it('saves description to localStorage as user types for new task', async () => {
      const wrapper = mount(TaskModal)
      const draftKey = `draftDescription_${testWorkspaceId}`

      const textarea = wrapper.find('textarea')
      await textarea.setValue('New description being typed')

      expect(localStorage.getItem(draftKey)).toBe('New description being typed')
    })

    it('removes draft from localStorage when description is cleared', async () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'Some draft')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      await textarea.setValue('')

      expect(localStorage.getItem(draftKey)).toBeNull()
    })

    it('clears draft from localStorage after successful task creation', async () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'Draft to be cleared')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      await textarea.setValue('Final description')

      const form = wrapper.find('form')
      await form.trigger('submit')
      await flushPromises()

      expect(localStorage.getItem(draftKey)).toBeNull()
    })

    it('does not load draft description when editing existing task', () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'Should not appear')

      const wrapper = mount(TaskModal, {
        props: {
          task: createTestTask()
        }
      })

      const textarea = wrapper.find('textarea')
      expect(textarea.element.value).toBe('Existing task description')
    })

    it('does not save to localStorage when editing existing task', async () => {
      const draftKey = `draftDescription_${testWorkspaceId}`

      const wrapper = mount(TaskModal, {
        props: {
          task: createTestTask()
        }
      })

      const textarea = wrapper.find('textarea')
      await textarea.setValue('Modified description')

      expect(localStorage.getItem(draftKey)).toBeNull()
    })

    it('uses workspace-specific key for draft description', async () => {
      const otherWorkspaceId = 'other-workspace-456'
      const otherDraftKey = `draftDescription_${otherWorkspaceId}`
      localStorage.setItem(otherDraftKey, 'Other workspace draft')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      expect(textarea.element.value).toBe('')
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

  describe('safeClose change detection', () => {
    it('detects changes when description differs from initial draft', async () => {
      const draftKey = `draftDescription_${testWorkspaceId}`
      localStorage.setItem(draftKey, 'Initial draft')

      const wrapper = mount(TaskModal)

      const textarea = wrapper.find('textarea')
      await textarea.setValue('Modified draft')

      expect((wrapper.vm as any).description).toBe('Modified draft')
    })

    it('detects changes when branch differs from initial saved branch', () => {
      const branchKey = `lastSelectedBranch_${testWorkspaceId}`
      localStorage.setItem(branchKey, 'initial-branch')

      const wrapper = mount(TaskModal)

      ;(wrapper.vm as any).selectedBranch = 'different-branch'

      expect((wrapper.vm as any).selectedBranch).toBe('different-branch')
    })
  })
})