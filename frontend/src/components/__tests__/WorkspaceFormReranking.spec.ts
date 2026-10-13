import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { RerankingConfig } from '@/lib/models'
import WorkspaceForm from '../WorkspaceForm.vue'

vi.mock('@/lib/profiles', () => ({
  DEFAULT_PROFILE_ID: 'default',
  useProfiles: () => ({
    profiles: { value: [] },
    profileName: (id: string) => id,
  }),
}))

afterEach(() => vi.unstubAllGlobals())

describe('WorkspaceForm reranking configuration', () => {
  it.each<RerankingConfig>([
    {
      defaults: [{ provider: 'company-ranking', model: 'rerank-v3.5' }],
      useCaseConfigs: { diff_summarize: [{ provider: 'cohere', model: 'rerank-v4.0-pro' }] },
    },
    { defaults: [], useCaseConfigs: {} },
    { defaults: null, useCaseConfigs: null },
  ])('preserves existing settings when editing a workspace', async (rerankingConfig) => {
    const workspace = {
      id: 'ws',
      name: 'Workspace',
      localRepoDir: '/repo',
      rerankingConfig,
    }
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ workspace }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = shallowMount(WorkspaceForm, { props: { workspace } })

    await wrapper.get('#name').setValue('Renamed')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledOnce()
    const body = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(body.name).toBe('Renamed')
    expect(body.rerankingConfig).toEqual(rerankingConfig)
    wrapper.unmount()
  })
})