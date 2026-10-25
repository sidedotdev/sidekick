import { createApp, h, nextTick, ref, type Component, type VNode } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { FlowAction } from '../lib/models'
import { parseDiff } from '../lib/diffUtils'
import ApplyEditBlocksFlowAction from './ApplyEditBlocksFlowAction.vue'
import DiffFile from './DiffFile.vue'
import FlowActionErrorBoundary from './FlowActionErrorBoundary.vue'
import FlowActionItem from './FlowActionItem.vue'
import SubflowContainer from './SubflowContainer.vue'
import UserRequest from './UserRequest.vue'

interface HmrRuntime {
  createRecord(id: string, component: Component): boolean
  rerender(id: string, render: () => VNode): void
}

const browserRuntime = vi.hoisted(() => ({ hmr: undefined as HmrRuntime | undefined }))

// Vue's Node build disables useCssVars, hiding browser-only mounted-hook failures.
vi.mock('vue', async () => {
  const vue = await vi.importActual<typeof import('vue')>('vue/dist/vue.esm-browser.js')
  browserRuntime.hmr = (
    globalThis as typeof globalThis & { __VUE_HMR_RUNTIME__: HmrRuntime }
  ).__VUE_HMR_RUNTIME__
  return vue
})

describe('flow action mounting', () => {
  const flowAction: FlowAction = {
    id: 'request',
    flowId: 'flow',
    workspaceId: 'workspace',
    created: new Date(),
    updated: new Date(),
    actionType: 'user_request',
    actionStatus: 'complete',
    actionParams: { requestKind: 'free_form' },
    actionResult: '{"Content":"Done"}',
    subflow: 'test',
    isHumanAction: true,
  }

  const cases: Array<{
    name: string
    component: Component
    props: Record<string, unknown>
    selector: string
    property: string
    expected: (level: number) => string
  }> = [
    {
      name: 'user request',
      component: UserRequest,
      props: {
        flowAction: {
          ...flowAction,
          actionParams: { requestKind: 'free_form', requestContent: 'Review these changes' },
        },
        expand: true,
      },
      selector: '.markdown-container',
      property: '--level',
      expected: String,
    },
    {
      name: 'action wrapper',
      component: FlowActionItem,
      props: { flowAction, defaultExpanded: true },
      selector: '.flow-action',
      property: '--level',
      expected: String,
    },
    {
      name: 'edit block results',
      component: ApplyEditBlocksFlowAction,
      props: {
        expand: true,
        flowAction: {
          ...flowAction,
          actionType: 'apply_edit_blocks',
          actionResult: JSON.stringify([{
            didApply: true,
            error: '',
            originalEditBlocks: [{
              filePath: 'file.txt',
              sequenceNumber: 1,
              oldLines: ['old'],
              newLines: ['new'],
            }],
          }]),
        },
      },
      selector: '.apply-edit-blocks-results',
      property: '--level',
      expected: String,
    },
    {
      name: 'subflow',
      component: SubflowContainer,
      props: { subflowTree: { name: 'Subflow', children: [] } },
      selector: '.subflow-container',
      property: '--subflow-level',
      expected: String,
    },
    {
      name: 'diff header',
      component: DiffFile,
      props: {
        fileData: parseDiff(
          'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n',
        )[0],
      },
      selector: '.file-header',
      property: 'top',
      expected: level => `calc(${level} * 2.5rem - 0.1rem)`,
    },
  ]

  it.each(cases)(
    'remounts $name without an action error when hot rerendering interrupts mounting',
    async ({ name, component, props, selector, property, expected }) => {
      const hmr = browserRuntime.hmr!
      const show = ref(true)
      const level = ref(2)
      const report = vi.spyOn(console, 'error').mockImplementation(() => {})
      const uncaught = vi.fn()
      const container = document.createElement('div')
      const Host = {
        __hmrId: `flow-action-mounting-${name}`,
        render: (): VNode => h('div', [
          h('span', {
            onVnodeMounted: () => {
              show.value = false
              hmr.rerender(Host.__hmrId, Host.render)
            },
          }),
          h(FlowActionErrorBoundary, { actionId: flowAction.id }, {
            default: () => show.value ? h(component, { ...props, level: level.value }) : null,
          }),
        ]),
      }
      expect(hmr.createRecord(Host.__hmrId, Host)).toBe(true)
      const app = createApp(Host)
      app.config.errorHandler = uncaught

      try {
        app.mount(container)
        await nextTick()
        expect(report).not.toHaveBeenCalled()
        expect(uncaught).not.toHaveBeenCalled()
        expect(container.querySelector('[role="alert"]')).toBeNull()

        show.value = true
        await nextTick()
        const element = container.querySelector<HTMLElement>(selector)
        expect(element).not.toBeNull()
        expect(element!.style.getPropertyValue(property)).toBe(expected(2))

        level.value = 3
        await nextTick()
        expect(element!.style.getPropertyValue(property)).toBe(expected(3))
        expect(report).not.toHaveBeenCalled()
        expect(uncaught).not.toHaveBeenCalled()
        expect(container.querySelector('[role="alert"]')).toBeNull()
      } finally {
        app.unmount()
        report.mockRestore()
      }
    },
  )
})