import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, VueWrapper } from '@vue/test-utils'
import BranchSelector from '../BranchSelector.vue'

vi.mock('primevue/select', () => ({
  default: {
    name: 'Select',
    inheritAttrs: false,
    props: [
      'modelValue',
      'options',
      'optionLabel',
      'optionValue',
      'filter',
      'autoFilterFocus',
      'resetFilterOnHide',
      'filterFields',
      'filterPlaceholder',
    ],
    emits: ['update:modelValue', 'filter', 'hide'],
    methods: {
      hide() {},
    },
    template: '<div><slot name="value" :value="modelValue" /><slot name="footer" /></div>',
  },
}))

const workspaceId = 'ws-branch-selector'

const branches = [
  { name: 'main', isCurrent: true, isDefault: true },
  { name: 'feature/alpha', isCurrent: false, isDefault: false },
]

const createdBranch = { name: 'feature/new-thing', isCurrent: false, isDefault: false }

const branchListResponse = () => ({
  ok: true,
  json: () => Promise.resolve({ branches }),
} as Response)

let fetchMock: ReturnType<typeof vi.fn>

const mountSelector = async (props: Record<string, unknown> = {}) => {
  const wrapper = mount(BranchSelector, {
    props: { workspaceId, modelValue: 'main', ...props },
  })
  await flushPromises()
  return wrapper
}

const branchSelect = (wrapper: VueWrapper) => wrapper.findAllComponents({ name: 'Select' })[0]

const optionLabels = (wrapper: VueWrapper) =>
  branchSelect(wrapper).props('options').map((option: { label: string }) => option.label)

const createButton = (wrapper: VueWrapper) => wrapper.find('button.fuzzy-append-option')

describe('BranchSelector', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchMock = vi.fn(() => Promise.resolve(branchListResponse()))
    global.fetch = fetchMock as unknown as typeof fetch
  })

  it('offers branch creation alongside matching branches when creation is allowed', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'feature' })
    await wrapper.vm.$nextTick()

    expect(optionLabels(wrapper)).toEqual(['feature/alpha'])
    expect(createButton(wrapper).text()).toBe('Create branch "feature"')
  })

  it('offers branch creation when nothing matches', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'zzz-new' })
    await wrapper.vm.$nextTick()

    expect(optionLabels(wrapper)).toEqual([])
    expect(createButton(wrapper).text()).toBe('Create branch "zzz-new"')
  })

  it('does not offer creation when the filter exactly matches a branch', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'main' })
    await wrapper.vm.$nextTick()

    expect(optionLabels(wrapper)).toEqual(['main'])
    expect(createButton(wrapper).exists()).toBe(false)
  })

  it('does not offer creation when the option is disabled', async () => {
    const wrapper = await mountSelector()

    branchSelect(wrapper).vm.$emit('filter', { value: 'zzz-new' })
    await wrapper.vm.$nextTick()

    expect(optionLabels(wrapper)).toEqual([])
    expect(createButton(wrapper).exists()).toBe(false)
  })

  it('creates the branch from the chosen base and selects it', async () => {
    fetchMock.mockImplementation((url: RequestInfo | URL, options?: RequestInit) => {
      if (options?.method === 'POST') {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ branch: createdBranch }),
        } as Response)
      }
      return Promise.resolve(branchListResponse())
    })

    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'feature/new-thing' })
    await wrapper.vm.$nextTick()
    await createButton(wrapper).trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.create-branch-dialog').exists()).toBe(true)

    const baseSelect = wrapper.findAllComponents({ name: 'Select' })[1]
    expect(baseSelect.props('modelValue')).toBe('main')
    baseSelect.vm.$emit('update:modelValue', 'feature/alpha')
    await wrapper.vm.$nextTick()

    await wrapper.find('.create-branch-confirm').trigger('click')
    await flushPromises()

    const postCall = fetchMock.mock.calls.find(([, options]) => (options as RequestInit)?.method === 'POST')
    expect(postCall).toBeDefined()
    expect(postCall![0]).toBe(`/api/v1/workspaces/${workspaceId}/branches`)
    expect(JSON.parse((postCall![1] as RequestInit).body as string)).toEqual({
      name: 'feature/new-thing',
      baseBranch: 'feature/alpha',
    })

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted![emitted!.length - 1]).toEqual(['feature/new-thing'])
    expect(wrapper.find('.create-branch-dialog').exists()).toBe(false)
  })

  it('keeps the previous selection and closes when creation is cancelled', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'zzz-new' })
    await wrapper.vm.$nextTick()
    branchSelect(wrapper).vm.$emit('update:modelValue', '__create_branch__:zzz-new')
    await wrapper.vm.$nextTick()

    await wrapper.find('.create-branch-cancel').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.create-branch-dialog').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(fetchMock.mock.calls.every(([, options]) => (options as RequestInit)?.method !== 'POST')).toBe(true)
  })

  it('surfaces creation errors without closing the dialog', async () => {
    fetchMock.mockImplementation((_url: RequestInfo | URL, options?: RequestInit) => {
      if (options?.method === 'POST') {
        return Promise.resolve({
          ok: false,
          status: 409,
          json: () => Promise.resolve({ error: 'Branch zzz-new already exists' }),
        } as Response)
      }
      return Promise.resolve(branchListResponse())
    })

    const wrapper = await mountSelector({ allowCreate: true })

    branchSelect(wrapper).vm.$emit('filter', { value: 'zzz-new' })
    await wrapper.vm.$nextTick()
    branchSelect(wrapper).vm.$emit('update:modelValue', '__create_branch__:zzz-new')
    await wrapper.vm.$nextTick()

    await wrapper.find('.create-branch-confirm').trigger('click')
    await flushPromises()

    expect(wrapper.find('.create-branch-dialog').exists()).toBe(true)
    expect(wrapper.find('.create-branch-error').text()).toBe('Branch zzz-new already exists')
  })
})