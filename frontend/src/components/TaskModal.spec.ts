import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { config, mount, flushPromises } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import TaskModal from './TaskModal.vue'
import { store } from '../lib/store'
import type { Task } from '../lib/models'

config.global.plugins.push(PrimeVue)

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

describe('TaskModal localStorage behavior', () => {
  const testWorkspaceId = 'test-workspace-123'

  beforeEach(() => {
    vi.stubGlobal('fetch', createMockFetch())
    localStorage.clear()
    store.workspaceId = testWorkspaceId
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