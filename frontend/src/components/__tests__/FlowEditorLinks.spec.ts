import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowEditorLinks from '../FlowEditorLinks.vue'

const mountLinks = (props: Record<string, unknown> = {}) => mount(FlowEditorLinks, {
  props: {
    flowId: 'flow-1',
    ...props,
  },
  global: {
    stubs: {
      RouterLink: true,
    },
  },
})

describe('FlowEditorLinks', () => {
  it('renders the accessible model configuration action alongside worktree links', async () => {
    const wrapper = mountLinks({
      showModelConfiguration: true,
      worktrees: [{
        id: 'worktree-1',
        workingDirectory: '/repo/worktree',
        created: new Date(),
        workspaceId: 'workspace-1',
      }],
    })

    const button = wrapper.get('[aria-label="Model configuration"]')
    expect(button.attributes('title')).toBe('Model configuration')
    expect(wrapper.get('.toolbar').text()).toContain('Open Worktree')

    await button.trigger('click')
    expect(wrapper.emitted('model-configuration')).toHaveLength(1)
  })

  it.each([
    { ide: 'vscode', label: 'Open worktree in VSCode' },
    { ide: 'intellij', label: 'Open worktree in IntelliJ' },
    { ide: 'zed', label: 'Open worktree in Zed' },
    { ide: 'vimr', label: 'Open worktree in VimR' },
  ])('opens the worktree in $ide through the API server', async ({ ide, label }) => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ success: true }), { status: 200 }),
    )

    const wrapper = mountLinks({
      worktrees: [{
        id: 'worktree-1',
        workingDirectory: '/repo/worktree',
        created: new Date(),
        workspaceId: 'workspace-1',
      }],
    })

    await wrapper.get(`[aria-label="${label}"]`).trigger('click')
    await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalled())

    const [url, options] = fetchSpy.mock.calls[0]
    expect(url).toBe('/api/v1/open-in-ide')
    expect(JSON.parse(options?.body as string)).toEqual({
      ide,
      filePath: '/repo/worktree',
    })

    vi.restoreAllMocks()
  })

  it('omits the action when the flow view does not enable it', () => {
    const wrapper = mountLinks()
    expect(wrapper.find('[aria-label="Model configuration"]').exists()).toBe(false)
  })

  it('retains the embedded toolbar offset', () => {
    const wrapper = mountLinks({
      subtask: true,
      showModelConfiguration: true,
    })

    expect(wrapper.get('.editor-links').classes()).toContain('editor-links-subtask')
    expect(wrapper.find('[aria-label="Model configuration"]').exists()).toBe(true)
  })
})