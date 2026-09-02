// BranchSelector.spec.ts mocks primevue/select module-wide, so it can only
// assert on props. These tests use the real Select to cover what actually
// renders in the dropdown.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import BranchSelector from '../BranchSelector.vue'

const workspaceId = 'ws-branch-selector-integration'

const branches = [
  { name: 'main', isCurrent: true, isDefault: true },
  { name: 'feature/alpha', isCurrent: false, isDefault: false },
]

const mountSelector = async (props: Record<string, unknown> = {}) => {
  const wrapper = mount(BranchSelector, {
    props: { workspaceId, modelValue: 'main', ...props },
    attachTo: document.body,
    global: { plugins: [PrimeVue] },
  })
  await flushPromises()
  return wrapper
}

const typeFilter = async (text: string) => {
  const filterInput = document.querySelector('input.p-select-filter') as HTMLInputElement | null
  expect(filterInput).toBeTruthy()
  for (const char of text) {
    filterInput!.value += char
    filterInput!.dispatchEvent(new Event('input'))
    await flushPromises()
  }
}

const optionLabels = () =>
  Array.from(document.querySelectorAll('li.p-select-option')).map(option => option.textContent?.trim())

describe('BranchSelector with the real PrimeVue Select', () => {
  beforeEach(() => {
    sessionStorage.clear()
    document.body.innerHTML = ''
    global.fetch = vi.fn(() => Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ branches }),
    } as Response)) as unknown as typeof fetch
  })

  const createButton = () =>
    document.querySelector('button.fuzzy-append-option') as HTMLButtonElement | null

  it('renders the create control alongside matching branches', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('feature')

    expect(optionLabels()).toEqual(['feature/alpha'])
    expect(createButton()?.textContent).toBe('Create branch "feature"')
  })

  it('keeps the create control actionable when the list shows no results', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('zzz-new')

    expect(optionLabels()).toEqual([])
    expect(document.body.textContent).toContain('No results found')

    createButton()!.click()
    await flushPromises()

    expect(wrapper.find('.create-branch-dialog').exists()).toBe(true)
    expect(wrapper.find('.create-branch-name').text()).toBe('zzz-new')
  })

  it('opens the create dialog when Enter is pressed on a non-existent branch name', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('zzz-new')

    const filterInput = document.querySelector('input.p-select-filter') as HTMLInputElement
    filterInput.dispatchEvent(new KeyboardEvent('keydown', { code: 'Enter', key: 'Enter', bubbles: true }))
    await flushPromises()

    expect(wrapper.find('.create-branch-dialog').exists()).toBe(true)
    expect(wrapper.find('.create-branch-name').text()).toBe('zzz-new')
  })

  it('does not offer creation for an exact existing branch name', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('main')

    expect(optionLabels()).toEqual(['main'])
    expect(createButton()).toBeNull()
  })

  it('omits the create control when creation is not allowed', async () => {
    const wrapper = await mountSelector()

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('zzz-new')

    expect(optionLabels()).toEqual([])
    expect(createButton()).toBeNull()
  })

  it('renders the create control when the prop is set as a bare attribute', async () => {
    const parent = {
      components: { BranchSelector },
      data: () => ({ branch: 'main' as string | null }),
      template: `<BranchSelector :workspace-id="'${workspaceId}'" v-model="branch" allow-create />`,
    }
    const wrapper = mount(parent, { attachTo: document.body, global: { plugins: [PrimeVue] } })
    await flushPromises()

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()
    await typeFilter('zzz-new')

    expect(createButton()?.textContent).toBe('Create branch "zzz-new"')
  })

  it('hints at branch creation in the filter placeholder', async () => {
    const wrapper = await mountSelector({ allowCreate: true })

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()

    const filterInput = document.querySelector('input.p-select-filter') as HTMLInputElement
    expect(filterInput.placeholder).toBe('Search or create branch')
  })

  it('keeps the plain search placeholder when creation is not allowed', async () => {
    const wrapper = await mountSelector()

    await wrapper.find('.p-select').trigger('click')
    await flushPromises()

    const filterInput = document.querySelector('input.p-select-filter') as HTMLInputElement
    expect(filterInput.placeholder).toBe('Search branches')
  })
})