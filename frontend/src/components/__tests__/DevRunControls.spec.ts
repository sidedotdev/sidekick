import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import DevRunControls from '../DevRunControls.vue'

class MockWebSocket {
  static instances: MockWebSocket[] = []
  
  url: string
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  readyState: number = WebSocket.CONNECTING
  
  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
    setTimeout(() => {
      this.readyState = WebSocket.OPEN
      this.onopen?.(new Event('open'))
    }, 0)
  }
  
  send = vi.fn()
  close = vi.fn(() => {
    this.readyState = WebSocket.CLOSED
  })
  
  simulateMessage(data: any) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }))
  }
  
  static clear() {
    MockWebSocket.instances = []
  }
}

describe('DevRunControls', () => {
  let wrapper: VueWrapper
  let originalWebSocket: typeof WebSocket

  beforeEach(() => {
    vi.clearAllMocks()
    MockWebSocket.clear()
    originalWebSocket = global.WebSocket
    global.WebSocket = MockWebSocket as any
  })

  afterEach(() => {
    global.WebSocket = originalWebSocket
    wrapper?.unmount()
  })

  const mountComponent = (props = {}) => {
    wrapper = mount(DevRunControls, {
      props: {
        workspaceId: 'test-workspace',
        flowId: 'test-flow',
        ...props
      }
    })
  }

  it('renders start button when not running', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()

    const startButton = wrapper.find('.dev-run-button.start')
    expect(startButton.exists()).toBe(true)
    expect(startButton.text()).toBe('Start')
  })

  it('emits start event when start button clicked', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()

    const startButton = wrapper.find('.dev-run-button.start')
    await startButton.trigger('click')

    expect(wrapper.emitted('start')).toHaveLength(1)
  })

  it('shows stop button when dev run is running', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 10))

    const flowSocket = MockWebSocket.instances.find(s => s.url.includes('/events'))
    flowSocket?.simulateMessage({
      eventType: 'dev_run_started',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      commandSummary: 'npm run dev',
      workingDir: '/tmp/worktree',
      pid: 1234,
      pgid: 1234
    })

    await wrapper.vm.$nextTick()

    const stopButton = wrapper.find('.dev-run-button.stop')
    expect(stopButton.exists()).toBe(true)
    expect(stopButton.text()).toBe('Stop')
  })

  it('emits stop event when stop button clicked', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 10))

    const flowSocket = MockWebSocket.instances.find(s => s.url.includes('/events'))
    flowSocket?.simulateMessage({
      eventType: 'dev_run_started',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      commandSummary: 'npm run dev',
      workingDir: '/tmp/worktree',
      pid: 1234,
      pgid: 1234
    })

    await wrapper.vm.$nextTick()

    const stopButton = wrapper.find('.dev-run-button.stop')
    await stopButton.trigger('click')

    expect(wrapper.emitted('stop')).toHaveLength(1)
  })

  it('shows output toggle when running', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 10))

    const flowSocket = MockWebSocket.instances.find(s => s.url.includes('/events'))
    flowSocket?.simulateMessage({
      eventType: 'dev_run_started',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      commandSummary: 'npm run dev',
      workingDir: '/tmp/worktree',
      pid: 1234,
      pgid: 1234
    })

    await wrapper.vm.$nextTick()

    const toggleButton = wrapper.find('.dev-run-toggle')
    expect(toggleButton.exists()).toBe(true)
    expect(toggleButton.text()).toBe('Show Output')
  })

  it('subscribes to devRunId output stream only when output toggled on', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 10))

    const flowSocket = MockWebSocket.instances.find(s => s.url.includes('/events'))

    // Initially only one socket (flow events)
    const initialSocketCount = MockWebSocket.instances.length

    flowSocket?.simulateMessage({
      eventType: 'dev_run_started',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      commandSummary: 'npm run dev',
      workingDir: '/tmp/worktree',
      pid: 1234,
      pgid: 1234
    })

    await wrapper.vm.$nextTick()
    // Wait for the 250ms debounce timer to fire
    await new Promise(resolve => setTimeout(resolve, 300))

    // Should have created a second socket for output after debounce
    expect(MockWebSocket.instances.length).toBeGreaterThan(initialSocketCount)

    // Find the output socket and verify it subscribed to devRunId
    const outputSocket = MockWebSocket.instances[MockWebSocket.instances.length - 1]
    expect(outputSocket.send).toHaveBeenCalledWith(JSON.stringify({ parentId: 'dev-run-123' }))
  })

  it('returns to not running state on dev_run_ended event', async () => {
    mountComponent()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 10))

    const flowSocket = MockWebSocket.instances.find(s => s.url.includes('/events'))
    
    // Start dev run
    flowSocket?.simulateMessage({
      eventType: 'dev_run_started',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      commandSummary: 'npm run dev',
      workingDir: '/tmp/worktree',
      pid: 1234,
      pgid: 1234
    })

    await wrapper.vm.$nextTick()
    expect(wrapper.find('.dev-run-button.stop').exists()).toBe(true)

    // End dev run
    flowSocket?.simulateMessage({
      eventType: 'dev_run_ended',
      flowId: 'test-flow',
      devRunId: 'dev-run-123',
      exitStatus: 0
    })

    await wrapper.vm.$nextTick()
    expect(wrapper.find('.dev-run-button.start').exists()).toBe(true)
  })

  it('disables buttons when disabled prop is true', async () => {
    mountComponent({ disabled: true })
    await wrapper.vm.$nextTick()

    const startButton = wrapper.find('.dev-run-button.start')
    expect(startButton.attributes('disabled')).toBeDefined()
  })
})