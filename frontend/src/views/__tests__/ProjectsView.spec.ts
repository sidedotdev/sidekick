import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import ProjectsView from '../ProjectsView.vue'
import { store } from '@/lib/store'
import type { Project, ProjectPriority } from '@/lib/models'

const project = (id: string, priority: ProjectPriority, rank: string, title = id): Project => ({
  workspaceId: 'ws1',
  id,
  title,
  priority,
  rank,
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
})

const initialProjects = [
  project('p1', 'urgent', 'n', 'Urgent One'),
  project('p2', 'high', 'n', 'High One'),
  project('p3', 'high', 'u', 'High Two'),
  project('p4', 'none', 'n', 'Plain One'),
]

describe('ProjectsView', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  let projects: Project[]

  beforeEach(() => {
    store.selectWorkspaceId('ws1')
    projects = [...initialProjects]
    fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (method === 'GET') {
        return { ok: true, json: async () => ({ projects }) }
      }
      return { ok: true, json: async () => ({}) }
    })
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('confirm', vi.fn(() => true))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const mountView = () =>
    mount(ProjectsView, {
      global: {
        plugins: [PrimeVue],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    })

  it('groups projects into non-empty priority buckets, most urgent first', async () => {
    const wrapper = mountView()
    await flushPromises()

    const sections = wrapper.findAll('.priority-bucket')
    expect(sections.map(s => s.attributes('data-priority'))).toEqual([
      'urgent',
      'high',
      'none',
    ])
    const highTitles = sections[1].findAll('.project-title').map(t => t.text())
    expect(highTitles).toEqual(['High One', 'High Two'])
    expect(sections[0].text()).toContain('Urgent One')
    expect(sections[2].text()).toContain('Plain One')
  })

  it('deletes a project after confirmation', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('button[aria-label="Delete Plain One"]').trigger('click')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/ws1/projects/p4',
      expect.objectContaining({ method: 'DELETE' })
    )
    expect(wrapper.text()).not.toContain('Plain One')
  })

  it('reorders a project within its bucket via drag and drop', async () => {
    const wrapper = mountView()
    await flushPromises()

    const highItems = wrapper.findAll('.priority-bucket[data-priority="high"] .project-item')
    await highItems[1].trigger('dragstart')
    await highItems[0].trigger('drop')
    await flushPromises()

    const putCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')
    expect(putCall).toBeDefined()
    expect(putCall![0]).toBe('/api/v1/workspaces/ws1/projects/p3')
    const body = JSON.parse(putCall![1]!.body as string)
    expect(body.priority).toBe('high')
    expect(body.rank < 'n').toBe(true)
  })

  it('ignores drops into a different priority bucket', async () => {
    const wrapper = mountView()
    await flushPromises()

    const urgentItem = wrapper.find('.priority-bucket[data-priority="urgent"] .project-item')
    await urgentItem.trigger('dragstart')
    await wrapper.find('.priority-bucket[data-priority="high"]').trigger('drop')
    await urgentItem.trigger('dragstart')
    await wrapper.find('.priority-bucket[data-priority="high"] .project-item').trigger('drop')
    await flushPromises()

    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(false)
  })
})