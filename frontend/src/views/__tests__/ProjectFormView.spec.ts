import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import ProjectFormView from '../ProjectFormView.vue'
import { store } from '@/lib/store'
import type { Project, ProjectPriority } from '@/lib/models'

const pushMock = vi.fn()
let routeParams: Record<string, string> = {}

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: routeParams }),
  useRouter: () => ({ push: pushMock }),
}))

const project = (id: string, priority: ProjectPriority, rank: string, title = id): Project => ({
  workspaceId: 'ws1',
  id,
  title,
  priority,
  rank,
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
})

describe('ProjectFormView', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  let projects: Project[]

  beforeEach(() => {
    store.selectWorkspaceId('ws1')
    pushMock.mockClear()
    routeParams = {}
    projects = [project('p1', 'high', 'u', 'Existing Project')]
    fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (method === 'GET') {
        return { ok: true, json: async () => ({ projects }) }
      }
      return { ok: true, json: async () => ({ project: {} }) }
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const mountView = () =>
    mount(ProjectFormView, {
      global: { plugins: [PrimeVue] },
    })

  it('creates a project ranked at the end of its priority bucket', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('h1').text()).toBe('New Project')

    await wrapper.find('#project-title').setValue('My Project')
    await wrapper.find('#project-description').setValue('Something new')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const postCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')
    expect(postCall).toBeDefined()
    expect(postCall![0]).toBe('/api/v1/workspaces/ws1/projects')
    const body = JSON.parse(postCall![1]!.body as string)
    expect(body.title).toBe('My Project')
    expect(body.description).toBe('Something new')
    expect(body.priority).toBe('none')
    expect(body.rank.length).toBeGreaterThan(0)
    expect(pushMock).toHaveBeenCalledWith({ name: 'projects' })
  })

  it('requires a title before creating', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    expect(wrapper.text()).toContain('Title is required')
  })

  it('edits an existing project, keeping its rank when priority is unchanged', async () => {
    routeParams = { id: 'p1' }
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('h1').text()).toBe('Edit Project')
    const titleInput = wrapper.find('#project-title').element as HTMLInputElement
    expect(titleInput.value).toBe('Existing Project')

    await wrapper.find('#project-title').setValue('Renamed Project')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const putCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')
    expect(putCall).toBeDefined()
    expect(putCall![0]).toBe('/api/v1/workspaces/ws1/projects/p1')
    const body = JSON.parse(putCall![1]!.body as string)
    expect(body.title).toBe('Renamed Project')
    expect(body.priority).toBe('high')
    expect(body.rank).toBe('u')
    expect(pushMock).toHaveBeenCalledWith({ name: 'projects' })
  })

  it('shows a not-found message for a missing project', async () => {
    routeParams = { id: 'missing' }
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Project not found')
    expect(wrapper.find('form').exists()).toBe(false)
  })
})