import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import Tooltip from '../Tooltip.vue'
import { TOOLTIP_OPEN_DELAY_MS, TOOLTIP_WARM_WINDOW_MS } from '../../composables/useTooltipTrigger'

const TwoTooltips = defineComponent({
  components: { Tooltip },
  template: `
    <div>
      <Tooltip class="first"><button>A</button><template #content>First tip</template></Tooltip>
      <Tooltip class="second"><button>B</button><template #content>Second tip</template></Tooltip>
    </div>
  `,
})

describe('Tooltip', () => {
  let wrapper: VueWrapper

  const popupText = () => wrapper.findAll('.tooltip-popup').map((popup) => popup.text())

  beforeEach(() => {
    vi.useFakeTimers()
    wrapper = mount(TwoTooltips)
  })

  afterEach(() => {
    wrapper.unmount()
    vi.runOnlyPendingTimers()
    vi.useRealTimers()
  })

  it('waits for the open delay before showing on hover', async () => {
    await wrapper.find('.first').trigger('mouseenter')
    await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS - 1)
    expect(popupText()).toEqual([])

    await vi.advanceTimersByTimeAsync(1)
    expect(popupText()).toEqual(['First tip'])
  })

  it('does not show when the pointer leaves before the delay elapses', async () => {
    await wrapper.find('.first').trigger('mouseenter')
    await wrapper.find('.first').trigger('mouseleave')
    await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS)
    expect(popupText()).toEqual([])
  })

  it('shows neighbouring tooltips instantly once one has been shown', async () => {
    await wrapper.find('.first').trigger('mouseenter')
    await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS)
    await wrapper.find('.first').trigger('mouseleave')
    await vi.advanceTimersByTimeAsync(TOOLTIP_WARM_WINDOW_MS - 1)

    await wrapper.find('.second').trigger('mouseenter')
    expect(popupText()).toEqual(['Second tip'])
  })

  it('restores the open delay after the pointer has been away long enough', async () => {
    await wrapper.find('.first').trigger('mouseenter')
    await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS)
    await wrapper.find('.first').trigger('mouseleave')
    await vi.advanceTimersByTimeAsync(TOOLTIP_WARM_WINDOW_MS)

    await wrapper.find('.second').trigger('mouseenter')
    expect(popupText()).toEqual([])
    await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS)
    expect(popupText()).toEqual(['Second tip'])
  })

  it('shows immediately on keyboard focus', async () => {
    await wrapper.find('.first button').trigger('focusin')
    expect(popupText()).toEqual(['First tip'])
  })
})
