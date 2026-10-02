import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import FlowActionErrorBoundary from './FlowActionErrorBoundary.vue'

describe('FlowActionErrorBoundary', () => {
  it.each(['setup', 'render', 'update'])(
    'isolates a %s failure while other actions keep updating',
    async (phase) => {
      const fail = ref(phase !== 'update')
      const progress = ref('Streaming')
      const showNext = ref(false)
      const failure = new Error('Broken action renderer')
      const report = vi.spyOn(console, 'error').mockImplementation(() => {})
      // Vue warns about the missing render function when setup throws.
      const vueWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const uncaught = vi.fn()
      const BrokenAction = defineComponent({
        setup() {
          if (phase === 'setup') throw failure
          return () => {
            if (fail.value) throw failure
            return h('p', 'Starting action')
          }
        },
      })
      const Host = defineComponent({
        setup: () => () => h('div', [
          h(FlowActionErrorBoundary, { actionId: 'broken' }, {
            default: () => h(BrokenAction),
          }),
          h(FlowActionErrorBoundary, { actionId: 'healthy' }, {
            default: () => h('p', progress.value),
          }),
          showNext.value ? h('p', 'Next action') : null,
        ]),
      })
      const wrapper = mount(Host, {
        global: { config: { errorHandler: uncaught } },
      })

      try {
        await nextTick()
        if (phase === 'update') {
          expect(wrapper.text()).toContain('Starting action')
          fail.value = true
          await nextTick()
          await nextTick()
        }

        expect(wrapper.get('[role="alert"]').text()).toContain('Unable to display this action')
        expect(uncaught).not.toHaveBeenCalled()
        expect(report).toHaveBeenCalled()

        progress.value = 'Completed'
        showNext.value = true
        await nextTick()

        expect(wrapper.text()).toContain('Completed')
        expect(wrapper.text()).toContain('Next action')
        expect(wrapper.findAll('[role="alert"]')).toHaveLength(1)
      } finally {
        wrapper.unmount()
        report.mockRestore()
        vueWarn.mockRestore()
      }
    },
  )
})