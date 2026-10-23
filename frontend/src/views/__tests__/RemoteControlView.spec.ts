import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import RemoteControlView from '../RemoteControlView.vue'
import { store } from '@/lib/store'
import App from '@/App.vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import PrimeVue from 'primevue/config'

vi.mock('qrcode', () => ({
  default: {
    toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,qr'),
  },
}))

const device = {
  id: 'device-1',
  name: 'My Phone',
  created: '2024-01-01T00:00:00Z',
  lastUsed: '0001-01-01T00:00:00Z',
}

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status })

describe('RemoteControlView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    store.workspaceId = null
  })

  it.each([
    { active: 'one', expected: 'one' },
    { active: null, expected: 'two' },
  ])('includes workspace $expected when active workspace is $active', async ({ active, expected }) => {
    store.workspaceId = active
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      if (init?.method === 'POST') {
        return jsonResponse({ device, ticket: 'ticket-1', token: 'token-1' }, 201)
      }
      if (input === '/api/v1/workspaces') {
        return jsonResponse({ workspaces: [
          { id: 'one', updated: '2024-02-01T00:00:00Z' },
          { id: 'two', updated: '2024-01-01T00:00:00Z' },
        ] })
      }
      if (input === '/api/v1/workspaces/two/tasks/') {
        return jsonResponse({ tasks: [{ created: '2024-03-01T00:00:00Z' }] })
      }
      if (input === '/api/v1/workspaces/one/tasks/') {
        return jsonResponse({ tasks: [] })
      }
      return jsonResponse({ devices: [] })
    })

    const wrapper = mount(RemoteControlView)
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const QRCode = (await import('qrcode')).default
    expect(QRCode.toDataURL).toHaveBeenLastCalledWith(
      JSON.stringify({ ticket: 'ticket-1', token: 'token-1', workspaceId: expected }),
      expect.anything(),
    )
  })

  it.each([
    { ready: true, query: false },
    { ready: false, query: false },
    { ready: true, query: true },
    { ready: false, query: true },
  ])('uses entry selection with router ready=$ready and query=$query', async ({ ready, query }) => {
    sessionStorage.clear()
    localStorage.clear()
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      if (init?.method === 'POST') {
        return jsonResponse({ device, ticket: 'ticket', token: 'token' }, 201)
      }
      if (input === '/api/v1/workspaces') {
        return jsonResponse({ workspaces: [
          { id: 'a', name: 'Recent activity', updated: '2024-01-01T00:00:00Z' },
          { id: 'z', name: 'Newest workspace', updated: '2024-02-01T00:00:00Z' },
        ] })
      }
      if (input === '/api/v1/workspaces/a/tasks/') {
        return jsonResponse({ tasks: [{ created: '2024-03-01T00:00:00Z' }] })
      }
      return jsonResponse({ devices: [], tasks: [] })
    })
    const history = createMemoryHistory()
    const entryUrl = query ? '/remote-control?workspaceId=z' : '/remote-control'
    history.replace(entryUrl)
    const placeholder = { render: () => null }
    const router = createRouter({
      history,
      routes: [
        { path: '/remote-control', name: 'remote-control', component: RemoteControlView },
        // App's navigation links resolve against other app routes on mount.
        { path: '/:pathMatch(.*)*', component: placeholder },
      ],
    })
    if (ready) await router.push(entryUrl)
    const wrapper = mount(App, { global: { plugins: [router, PrimeVue] } })
    await router.isReady()
    await flushPromises()
    expect(store.workspaceId).toBe('z')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const QRCode = (await import('qrcode')).default
    expect(QRCode.toDataURL).toHaveBeenLastCalledWith(
      JSON.stringify({ ticket: 'ticket', token: 'token', workspaceId: query ? 'z' : 'a' }),
      expect.anything(),
    )
    wrapper.unmount()
  })

  it('keeps the workspace from page entry when selection later changes', async () => {
    store.workspaceId = 'entry'
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) =>
      jsonResponse(init?.method === 'POST'
        ? { device, ticket: 'ticket', token: 'token' }
        : { devices: [] }),
    )
    const wrapper = mount(RemoteControlView)
    await flushPromises()
    store.workspaceId = 'later'
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const QRCode = (await import('qrcode')).default
    expect(QRCode.toDataURL).toHaveBeenLastCalledWith(
      JSON.stringify({ ticket: 'ticket', token: 'token', workspaceId: 'entry' }),
      expect.anything(),
    )
    wrapper.unmount()
  })

  it('lists paired devices, showing "never" for unused ones', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ devices: [device] }))

    const wrapper = mount(RemoteControlView)
    await flushPromises()

    expect(wrapper.text()).toContain('My Phone')
    expect(wrapper.text()).toContain('Last used never')
  })

  it('creates a pairing and displays its QR code', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
      if (init?.method === 'POST') {
        return jsonResponse({ device, ticket: 'ticket-1', token: 'token-1' }, 201)
      }
      return jsonResponse({ devices: [device] })
    })

    const wrapper = mount(RemoteControlView)
    await flushPromises()

    await wrapper.get('input').setValue('My Phone')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const postCall = fetchSpy.mock.calls.find(([, init]) => init?.method === 'POST')
    expect(postCall?.[0]).toBe('/api/v1/remote/pairings/')
    expect(JSON.parse(postCall?.[1]?.body as string)).toEqual({ name: 'My Phone' })

    expect(wrapper.get('img.qr-code').attributes('src')).toBe('data:image/png;base64,qr')

    const QRCode = (await import('qrcode')).default
    expect(QRCode.toDataURL).toHaveBeenCalledWith(
      JSON.stringify({ ticket: 'ticket-1', token: 'token-1' }),
      expect.anything(),
    )
  })

  it('unpairs a device after confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
      if (init?.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      return jsonResponse({ devices: [device] })
    })

    const wrapper = mount(RemoteControlView)
    await flushPromises()

    await wrapper.get('[aria-label="Unpair My Phone"]').trigger('click')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith('/api/v1/remote/pairings/device-1', { method: 'DELETE' })
    expect(wrapper.text()).not.toContain('My Phone')
  })

  it('surfaces the API error when pairing fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
      if (init?.method === 'POST') {
        return jsonResponse({ error: 'remote server is not running' }, 503)
      }
      return jsonResponse({ devices: [] })
    })

    const wrapper = mount(RemoteControlView)
    await flushPromises()

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('remote server is not running')
    expect(wrapper.find('img.qr-code').exists()).toBe(false)
  })
})