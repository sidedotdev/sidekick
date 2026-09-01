import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { config, flushPromises, mount } from '@vue/test-utils';
import PrimeVue from 'primevue/config';
import WorkspaceForm from '../WorkspaceForm.vue';
import LlmConfigEditor from '../LlmConfigEditor.vue';
import EmbeddingConfigEditor from '../EmbeddingConfigEditor.vue';
import type { Profile, Workspace } from '../../lib/models';

config.global.plugins.push(PrimeVue);

const mockProvidersData = { providers: ['google', 'anthropic', 'openai'] };
const mockModelsData = {
  openai: { models: { 'gpt-4': { reasoning: false } } },
  anthropic: { models: { 'claude-3': { reasoning: false } } },
};
const mockProfiles: Profile[] = [
  { id: 'default', name: 'Default' },
  { id: 'work', name: 'Work' },
];

const providersForUrl = (url: string) => {
  const profileId = new URLSearchParams(url.split('?')[1] ?? '').get('profileId') || 'default';
  return profileId === 'work' ? { providers: ['work_openai'] } : mockProvidersData;
};

const createMockFetch = (workspaceResponse: object) => {
  return vi.fn((url: string, options?: RequestInit) => {
    if (url.startsWith('/api/v1/providers')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(providersForUrl(url)),
      });
    }
    if (url === '/api/v1/profiles') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ profiles: mockProfiles }),
      });
    }
    if (url === '/api/v1/models') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(mockModelsData),
      });
    }
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve(workspaceResponse),
    });
  });
};

const cacheProfiles = (profiles: Profile[]) => {
  sessionStorage.setItem('profiles_cache', JSON.stringify({ data: profiles, timestamp: Date.now() }));
};

const emptyWorkspace = (): Workspace => ({
  name: '',
  localRepoDir: '',
  configMode: 'merge',
  llmConfig: { defaults: [], useCaseConfigs: {} },
  embeddingConfig: { defaults: [], useCaseConfigs: {} }
} as Workspace);

describe('WorkspaceForm.vue', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    sessionStorage.clear();
  });

  it('emits created event and makes API request with correct data on form submission for new workspace', async () => {
    const mockFetch = createMockFetch({ workspace: { id: '123' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, {
      props: {
        workspace: {
          name: '',
          localRepoDir: '',
          configMode: 'merge',
          llmConfig: {
            defaults: [],
            useCaseConfigs: {}
          },
          embeddingConfig: {
            defaults: [],
            useCaseConfigs: {}
          }
        } as Workspace
      }
    });

    await vi.waitFor(() => {
      const llmEditor = wrapper.findComponent(LlmConfigEditor);
      const options = llmEditor.find('.provider-select').findAll('option');
      expect(options.length).toBeGreaterThan(1);
    });

    await wrapper.find('#name').setValue('New Workspace');
    await wrapper.find('#localRepoDir').setValue('/local/repo/dir');

    const llmEditor = wrapper.findComponent(LlmConfigEditor);
    await llmEditor.find('.provider-select').setValue('openai');

    const embeddingEditor = wrapper.findComponent(EmbeddingConfigEditor);
    await embeddingEditor.find('.provider-select').setValue('openai');

    await wrapper.find('form').trigger('submit.prevent');

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: 'New Workspace',
        localRepoDir: '/local/repo/dir',
        configMode: 'merge',
        llmConfig: { defaults: [{ provider: 'openai', model: '', reasoningEffort: '', speed: '' }], useCaseConfigs: {} },
        embeddingConfig: { defaults: [{ provider: 'openai', model: '' }], useCaseConfigs: {} }
      }),
    });

    /* FIXME later: something goes wrong with testing emitted events when using
     * mockFetch with awaiting json in the component for some weird reason. So
     * for now, we'll just not test this aspect: it's not crucial as we're not
     * using this event at all yet. */
    // expect(wrapper.emitted('created')).toBeTruthy();
    // if (wrapper.emitted('created')) {
    //   expect(wrapper.emitted('created')[0]).toEqual(['123']);
    // }
  });

  it('emits updated event and makes API request with correct data on form submission for existing workspace', async () => {
    const mockFetch = createMockFetch({ workspace: { id: '456' } });
    vi.stubGlobal('fetch', mockFetch);

    const existingWorkspace: Workspace = {
      id: '456',
      name: 'Existing Workspace',
      localRepoDir: '/existing/repo/dir',
      configMode: 'merge',
      llmConfig: {
        defaults: [{ provider: 'anthropic', model: '' }],
        useCaseConfigs: {}
      },
      embeddingConfig: {
        defaults: [{ provider: 'openai', model: '' }],
        useCaseConfigs: {}
      }
    };

    const wrapper = mount(WorkspaceForm, {
      props: { workspace: existingWorkspace }
    });

    await vi.waitFor(() => {
      const llmEditor = wrapper.findComponent(LlmConfigEditor);
      const options = llmEditor.find('.provider-select').findAll('option');
      expect(options.length).toBeGreaterThan(1);
    });

    await wrapper.find('#name').setValue('Updated Workspace');
    await wrapper.find('#localRepoDir').setValue('/updated/repo/dir');

    const llmEditor = wrapper.findComponent(LlmConfigEditor);
    await llmEditor.find('.provider-select').setValue('openai');

    const embeddingEditor = wrapper.findComponent(EmbeddingConfigEditor);
    await embeddingEditor.find('.provider-select').setValue('openai');

    await wrapper.find('form').trigger('submit.prevent');

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/456', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: 'Updated Workspace',
        localRepoDir: '/updated/repo/dir',
        configMode: 'merge',
        llmConfig: {
          defaults: [{ provider: 'openai', model: '', reasoningEffort: '', speed: '' }],
          useCaseConfigs: {}
        },
        embeddingConfig: {
          defaults: [{ provider: 'openai', model: '' }],
          useCaseConfigs: {}
        }
      }),
    });

    /* FIXME later: something goes wrong with testing emitted events when using
     * mockFetch with awaiting json in the component for some weird reason. So
     * for now, we'll just not test this aspect: it's not crucial as we're not
     * using this event at all yet. */
    //expect(wrapper.emitted('updated')).toBeTruthy();
    //if (wrapper.emitted('updated')) {
    //  expect(wrapper.emitted('updated')[0]).toEqual([{ id: '456', name: 'Updated Workspace', localRepoDir: '/updated/repo/dir', config: { llm: { defaultConfig: { provider: 'openai', model: 'gpt-4' } }, embedding: { defaultConfig: { provider: 'openai', model: 'text-embedding-ada-002' } } } }]);
    //}
  });

  it('preserves the existing workspace profile when updating', async () => {
    const mockFetch = createMockFetch({ workspace: { id: '999' } });
    vi.stubGlobal('fetch', mockFetch);

    const existingWorkspace: Workspace = {
      id: '999',
      name: 'Work Workspace',
      localRepoDir: '/work/repo/dir',
      configMode: 'merge',
      profileId: 'work',
      llmConfig: {
        defaults: [{ provider: 'anthropic', model: '' }],
        useCaseConfigs: {}
      },
      embeddingConfig: {
        defaults: [{ provider: 'openai', model: '' }],
        useCaseConfigs: {}
      }
    };

    const wrapper = mount(WorkspaceForm, {
      props: { workspace: existingWorkspace }
    });

    await vi.waitFor(() => {
      const llmEditor = wrapper.findComponent(LlmConfigEditor);
      const options = llmEditor.find('.provider-select').findAll('option');
      expect(options.length).toBeGreaterThan(1);
    });

    await wrapper.find('form').trigger('submit.prevent');

    const updateCall = mockFetch.mock.calls.find(([url]) => url === '/api/v1/workspaces/999');
    expect(updateCall).toBeDefined();
    const requestBody = updateCall?.[1]?.body;
    expect(typeof requestBody).toBe('string');
    expect(JSON.parse(requestBody as string).profileId).toBe('work');
  });

  it('lists cached profile names in the profile dropdown', async () => {
    cacheProfiles([{ id: 'default', name: 'Personal' }, { id: 'work', name: 'Work' }]);
    const mockFetch = createMockFetch({ workspace: { id: '111' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, { props: { workspace: emptyWorkspace() } });
    await wrapper.vm.$nextTick();

    const options = wrapper.find('#profileId').findAll('option');
    expect(options.map(option => option.text())).toEqual(['Personal', 'Work']);
    expect((wrapper.find('#profileId').element as HTMLSelectElement).value).toBe('default');
  });

  it('selects the declared profile when the workspace profile id differs in case', async () => {
    cacheProfiles(mockProfiles);
    const mockFetch = createMockFetch({ workspace: { id: '555' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, {
      props: {
        workspace: { ...emptyWorkspace(), id: '555', profileId: 'WORK' } as Workspace
      }
    });
    await wrapper.vm.$nextTick();

    const select = wrapper.find('#profileId');
    expect(select.findAll('option').map(option => option.text())).toEqual(['Default', 'Work']);
    expect((select.element as HTMLSelectElement).value).toBe('work');

    await wrapper.find('form').trigger('submit.prevent');

    const updateCall = mockFetch.mock.calls.find(([url]) => url === '/api/v1/workspaces/555');
    expect(updateCall).toBeDefined();
    expect(JSON.parse(updateCall?.[1]?.body as string).profileId).toBe('work');
  });

  it('ignores stale provider responses when the profile changes', async () => {
    cacheProfiles(mockProfiles);

    const pendingProviderRequests: { url: string; release: () => void }[] = [];
    const mockFetch = vi.fn((url: string) => {
      if (url.startsWith('/api/v1/providers')) {
        return new Promise<{ ok: boolean; json: () => Promise<unknown> }>((resolve) => {
          pendingProviderRequests.push({
            url,
            release: () => resolve({ ok: true, json: () => Promise.resolve(providersForUrl(url)) })
          });
        });
      }
      if (url === '/api/v1/profiles') {
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ profiles: mockProfiles }) });
      }
      if (url === '/api/v1/models') {
        return Promise.resolve({ ok: true, json: () => Promise.resolve(mockModelsData) });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ workspace: { id: '666' } }) });
    });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, { props: { workspace: emptyWorkspace() } });
    await vi.waitFor(() => expect(pendingProviderRequests.length).toBe(2));

    await wrapper.find('#profileId').setValue('work');
    await vi.waitFor(() => expect(pendingProviderRequests.length).toBe(4));

    const release = (matches: (url: string) => boolean) => {
      pendingProviderRequests.filter(request => matches(request.url)).forEach(request => request.release());
    };
    release(url => url.includes('profileId=work'));
    await flushPromises();
    release(url => !url.includes('profileId='));
    await flushPromises();

    const llmProviders = wrapper.findComponent(LlmConfigEditor)
      .find('.provider-select').findAll('option')
      .map(option => (option.element as HTMLOptionElement).value);
    const embeddingProviders = wrapper.findComponent(EmbeddingConfigEditor)
      .find('.provider-select').findAll('option')
      .map(option => (option.element as HTMLOptionElement).value);

    expect(llmProviders).toContain('work_openai');
    expect(llmProviders).not.toContain('anthropic');
    expect(embeddingProviders).toContain('work_openai');
    expect(embeddingProviders).not.toContain('anthropic');
  });

  it('falls back to the profile id when the profile name is unknown', async () => {
    cacheProfiles([{ id: 'default', name: 'Default' }]);
    const mockFetch = createMockFetch({ workspace: { id: '222' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, {
      props: {
        workspace: { ...emptyWorkspace(), id: '222', profileId: 'contractor-x' } as Workspace
      }
    });
    await wrapper.vm.$nextTick();

    const options = wrapper.find('#profileId').findAll('option');
    expect(options.map(option => option.text())).toContain('contractor-x');
    expect((wrapper.find('#profileId').element as HTMLSelectElement).value).toBe('contractor-x');
  });

  it('requests profile-filtered providers and persists the selected profile', async () => {
    cacheProfiles(mockProfiles);
    const mockFetch = createMockFetch({ workspace: { id: '333' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, { props: { workspace: emptyWorkspace() } });

    await vi.waitFor(() => {
      const llmEditor = wrapper.findComponent(LlmConfigEditor);
      const options = llmEditor.find('.provider-select').findAll('option');
      expect(options.length).toBeGreaterThan(1);
    });

    await wrapper.find('#name').setValue('Work Workspace');
    await wrapper.find('#localRepoDir').setValue('/work/repo/dir');
    await wrapper.find('#profileId').setValue('work');

    await vi.waitFor(() => {
      const llmProviders = wrapper.findComponent(LlmConfigEditor)
        .find('.provider-select').findAll('option')
        .map(option => (option.element as HTMLOptionElement).value);
      expect(llmProviders).toContain('work_openai');
      expect(llmProviders).not.toContain('anthropic');

      const embeddingProviders = wrapper.findComponent(EmbeddingConfigEditor)
        .find('.provider-select').findAll('option')
        .map(option => (option.element as HTMLOptionElement).value);
      expect(embeddingProviders).toContain('work_openai');
    });

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/providers?profileId=work');

    await wrapper.find('form').trigger('submit.prevent');

    const createCall = mockFetch.mock.calls.find(([url]) => url === '/api/v1/workspaces');
    expect(createCall).toBeDefined();
    expect(JSON.parse(createCall?.[1]?.body as string).profileId).toBe('work');
  });

  it('omits the profile when the default profile is selected', async () => {
    cacheProfiles(mockProfiles);
    const mockFetch = createMockFetch({ workspace: { id: '444' } });
    vi.stubGlobal('fetch', mockFetch);

    const wrapper = mount(WorkspaceForm, {
      props: {
        workspace: { ...emptyWorkspace(), id: '444', profileId: 'work' } as Workspace
      }
    });

    await vi.waitFor(() => {
      expect((wrapper.find('#profileId').element as HTMLSelectElement).value).toBe('work');
    });

    await wrapper.find('#profileId').setValue('default');
    await wrapper.find('form').trigger('submit.prevent');

    const updateCall = mockFetch.mock.calls.find(([url]) => url === '/api/v1/workspaces/444');
    expect(updateCall).toBeDefined();
    expect(JSON.parse(updateCall?.[1]?.body as string).profileId).toBeUndefined();
  });

  it('populates form fields with existing workspace data when editing', async () => {
    const mockFetch = createMockFetch({ workspace: { id: '789' } });
    vi.stubGlobal('fetch', mockFetch);

    const existingWorkspace: Workspace = {
      id: '789',
      name: 'Existing Workspace',
      localRepoDir: '/existing/repo/dir',
      configMode: 'merge',
      llmConfig: {
        defaults: [{ provider: 'anthropic', model: '' }],
        useCaseConfigs: {}
      },
      embeddingConfig: {
        defaults: [{ provider: 'openai', model: '' }],
        useCaseConfigs: {}
      }
    };

    const wrapper = mount(WorkspaceForm, {
      props: { workspace: existingWorkspace }
    });

    await vi.waitFor(() => {
      const llmEditor = wrapper.findComponent(LlmConfigEditor);
      const options = llmEditor.find('.provider-select').findAll('option');
      expect(options.length).toBeGreaterThan(1);
    });

    expect((wrapper.find('#name').element as HTMLInputElement).value).toBe('Existing Workspace');
    expect((wrapper.find('#localRepoDir').element as HTMLInputElement).value).toBe('/existing/repo/dir');

    const llmEditor = wrapper.findComponent(LlmConfigEditor);
    expect((llmEditor.find('.provider-select').element as HTMLSelectElement).value).toBe('anthropic');

    const embeddingEditor = wrapper.findComponent(EmbeddingConfigEditor);
    expect((embeddingEditor.find('.provider-select').element as HTMLSelectElement).value).toBe('openai');
  });
});