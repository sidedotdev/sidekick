import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import RemoteControlView from '../RemoteControlView.vue'

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