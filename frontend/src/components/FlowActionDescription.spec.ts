import { createApp, h, nextTick } from 'vue'
import { describe, expect, it } from 'vitest'
import type { FlowAction } from '../lib/models'
import FlowActionItem from './FlowActionItem.vue'

describe('tool action descriptions', () => {
  const tools = [
    'get_symbol_definitions',
    'run_command',
    'bulk_search_repository',
    'read_file_lines',
    'read_image',
  ]
  const cases = [
    ...tools.map(tool => ({
      name: tool,
      actionType: `tool_call.${tool}`,
      actionParams: { description: '  Inspect permission handling  ' },
      heading: 'Inspect permission handling',
    })),
    ...[undefined, null, '', ' \n ', 42, { text: 'Not a string' }].map(description => ({
      name: `fallback for ${JSON.stringify(description)}`,
      actionType: 'tool_call.run_command',
      actionParams: { description },
      heading: 'Tool: run_command',
    })),
    {
      name: 'descriptions are plain text',
      actionType: 'tool_call.run_command',
      actionParams: { description: '<img src=x onerror=alert(1)>' },
      heading: '<img src=x onerror=alert(1)>',
    },
    {
      name: 'non-tool actions keep their existing heading',
      actionType: 'ranked_repo_summary',
      actionParams: { description: 'Unrelated content' },
      heading: 'Ranked Repo Summary',
    },
  ]

  it.each(cases)('$name', async ({ actionType, actionParams, heading }) => {
    const flowAction: FlowAction = {
      id: 'action',
      flowId: 'flow',
      workspaceId: 'workspace',
      created: new Date(),
      updated: new Date(),
      actionType,
      actionStatus: 'complete',
      actionParams,
      actionResult: '',
      subflow: 'test',
      isHumanAction: false,
    }
    const container = document.createElement('div')
    const app = createApp({
      render: () => h(FlowActionItem, { flowAction, defaultExpanded: false, level: 0 }),
    })

    try {
      app.mount(container)
      await nextTick()
      const header = container.querySelector('h3')
      expect(header?.textContent?.trim()).toBe(heading)
      expect(header?.querySelector('img')).toBeNull()
      if (actionType.startsWith('tool_call.')) {
        expect(header?.title).toContain(actionType.slice('tool_call.'.length))
      }
    } finally {
      app.unmount()
    }
  })
})