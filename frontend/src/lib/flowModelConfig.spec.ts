import { describe, expect, it } from 'vitest'
import { isFlowModelConfigVisible } from './flowModelConfig'

describe('flow model configuration visibility', () => {
  it.each(['started', 'paused'])(
    'shows the action for %s flows in development mode',
    (status) => {
      expect(isFlowModelConfigVisible(status, true)).toBe(true)
    },
  )

  it.each(['pending', 'completed', 'failed', 'canceled'])(
    'hides the action for unsupported %s flows',
    (status) => {
      expect(isFlowModelConfigVisible(status, true)).toBe(false)
    },
  )

  it.each(['started', 'paused'])(
    'omits the action for %s flows outside development mode',
    (status) => {
      expect(isFlowModelConfigVisible(status, false)).toBe(false)
    },
  )
})