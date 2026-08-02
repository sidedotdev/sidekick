import { describe, expect, it } from 'vitest'
import { isFlowModelConfigVisible } from './flowModelConfig'
import type { FlowStatus } from './models'

describe('flow model configuration visibility', () => {
  it.each<FlowStatus>(['in_progress', 'paused'])(
    'shows the action for %s flows in development mode',
    (status) => {
      expect(isFlowModelConfigVisible(status, true)).toBe(true)
    },
  )

  it.each<FlowStatus>(['completed', 'failed', 'canceled'])(
    'hides the action for terminal %s flows',
    (status) => {
      expect(isFlowModelConfigVisible(status, true)).toBe(false)
    },
  )

  it.each<FlowStatus>(['in_progress', 'paused'])(
    'omits the action for %s flows outside development mode',
    (status) => {
      expect(isFlowModelConfigVisible(status, false)).toBe(false)
    },
  )
})