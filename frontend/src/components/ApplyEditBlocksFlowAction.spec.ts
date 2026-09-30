import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import ApplyEditBlocksFlowAction from './ApplyEditBlocksFlowAction.vue'
import FlowActionErrorBoundary from './FlowActionErrorBoundary.vue'
import type { FlowAction } from '../lib/models'

// jsdom has no canvas; @git-diff-view measures text widths with one. Stub only
// that so the real diff renderer still runs.
let getContextSpy: ReturnType<typeof vi.spyOn>
beforeAll(() => {
  getContextSpy = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => ({
    font: '',
    measureText: (text: string) => ({ width: text.length * 7 }),
  }) as unknown as CanvasRenderingContext2D)
})
afterAll(() => {
  getContextSpy.mockRestore()
})

// Recovered from a real flow: git emits a combined diff (`diff --cc`, `@@@`
// hunk header, two prefix columns) when the edited file is unmerged during
// conflict resolution. The diff renderer library rejects this hunk format.
const combinedDiff = [
  'diff --cc Justfile',
  'index 07a3b20,2a11cd1..0000000',
  '--- a/Justfile',
  '+++ b/Justfile',
  '@@@ -7,5 -7,6 +7,9 @@@ install',
  '  release *args:',
  '  \t./scripts/release.sh {{args}}',
  '  ',
  ' +publish-web *args:',
  '- \t./scripts/publish-web.sh {{args}}',
  '++\t./scripts/publish-web.sh {{args}}',
  '++',
  '+ # Publish the empty npm package that reserves the `bgx` name; pass --dry-run to preview.',
  '+ publish-node-placeholder *args:',
  ' -\tcd node && npm publish {{args}}',
  '++\tcd node && npm publish {{args}}',
  '',
].join('\n')

const malformedDiff = [
  'diff --git a/foo.txt b/foo.txt',
  '--- a/foo.txt',
  '+++ b/foo.txt',
  '@@ not a real hunk header @@',
  '-old line',
  '+new line',
  '',
].join('\n')

const unifiedDiff = [
  'diff --git a/foo.txt b/foo.txt',
  '--- a/foo.txt',
  '+++ b/foo.txt',
  '@@ -1,2 +1,2 @@',
  ' unchanged',
  '-old line',
  '+new line',
  '',
].join('\n')

function flowActionWithDiff(finalDiff: string): FlowAction {
  return {
    id: 'fa_test',
    flowId: 'flow_test',
    workspaceId: 'ws_test',
    created: new Date(),
    updated: new Date(),
    subflow: 'Edit Code',
    actionType: 'apply_edit_blocks',
    actionStatus: 'complete',
    isHumanAction: false,
    actionParams: {},
    actionResult: JSON.stringify([{
      originalEditBlocks: [{ filePath: 'Justfile', oldLines: ['a'], newLines: ['b'], sequenceNumber: 1 }],
      didApply: true,
      error: '',
      checkResult: { success: true, message: '' },
      finalDiff,
    }]),
  } as FlowAction
}

async function settle() {
  await nextTick()
  await nextTick()
}

async function mountInBoundary(initialFlowAction: FlowAction) {
  const flowAction = ref(initialFlowAction)
  const Host = defineComponent({
    setup: () => () => h(FlowActionErrorBoundary, { actionId: flowAction.value.id }, {
      default: () => h(ApplyEditBlocksFlowAction, { flowAction: flowAction.value, expand: true }),
    }),
  })
  const wrapper = mount(Host, { attachTo: document.body })
  await settle()
  return { wrapper, flowAction }
}

describe('ApplyEditBlocksFlowAction diff rendering', () => {
  it('shows a diff the renderer rejects as the raw patch instead of failing the whole action', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      const { wrapper } = await mountInBoundary(flowActionWithDiff(malformedDiff))
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('Applied')
      expect(wrapper.get('.raw-diff').element.textContent).toBe(malformedDiff)
    } finally {
      consoleError.mockRestore()
    }
  })

  it('renders a combined merge-conflict diff as a two-way diff against a selectable parent', async () => {
    const { wrapper } = await mountInBoundary(flowActionWithDiff(combinedDiff))
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('.raw-diff').exists()).toBe(false)

    const optionLabels = () => wrapper.findAll('.combined-diff-bar .segmented-control button').map(b => b.text())
    expect(optionLabels()).toEqual(['HEAD (ours)', 'Incoming (theirs)', 'Raw'])

    // Against HEAD: publish-web target already existed, so only its recipe changed.
    let content = wrapper.get('.diff-content').text()
    expect(content).toContain('@@ -7,5 +7,9 @@')
    expect(content).toContain('publish-node-placeholder')
    expect(wrapper.get('.added-count').text()).toBe('+5')
    expect(wrapper.get('.removed-count').text()).toBe('-1')
    expect(content).not.toContain('@@@')

    await wrapper.findAll('.combined-diff-bar .segmented-control button')[1].trigger('click')
    await settle()
    content = wrapper.get('.diff-content').text()
    // Against the incoming branch: publish-web target is new, placeholder target is context.
    expect(content).toContain('@@ -7,6 +7,9 @@')
    expect(content).toContain('publish-web')
    expect(wrapper.get('.added-count').text()).toBe('+4')
    expect(wrapper.get('.removed-count').text()).toBe('-1')
    expect(wrapper.find('.raw-diff').exists()).toBe(false)

    await wrapper.findAll('.combined-diff-bar .segmented-control button')[2].trigger('click')
    await settle()
    expect(wrapper.get('.raw-diff').element.textContent).toBe(combinedDiff)
  })

  it('renders an ordinary unified diff with the rich diff viewer', async () => {
    const { wrapper } = await mountInBoundary(flowActionWithDiff(unifiedDiff))
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('.raw-diff').exists()).toBe(false)
    expect(wrapper.get('.diff-content').text()).toContain('new line')
  })

  it('returns to the rich viewer once a rejected diff is replaced by a valid one', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      const { wrapper, flowAction } = await mountInBoundary(flowActionWithDiff(malformedDiff))
      expect(wrapper.get('.raw-diff').element.textContent).toBe(malformedDiff)

      flowAction.value = flowActionWithDiff(unifiedDiff)
      await settle()

      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(wrapper.find('.raw-diff').exists()).toBe(false)
      expect(wrapper.get('.diff-content').text()).toContain('new line')
    } finally {
      consoleError.mockRestore()
    }
  })
})