import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PrimeVue from 'primevue/config'
import App from '../App.vue'
import { store } from '../lib/store'

describe('workspace shortcut', () => {
  let wrapper: VueWrapper

  beforeEach(async () => {
    sessionStorage.clear()
    localStorage.clear()
    store.workspaceId = 'alpha'
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      workspaces: [{ id: 'alpha', name: 'Alpha' }, { id: 'beta', name: 'Beta' }],
    })))
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/kanban', name: 'kanban', component: { render: () => null } },
        { path: '/:pathMatch(.*)*', component: { render: () => null } },
      ],
    })
    await router.push('/')
    wrapper = mount(App, {
      attachTo: document.body,
      global: { plugins: [router, PrimeVue] },
    })
    await flushPromises()
  })

  afterEach(() => {
    wrapper?.unmount()
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })

  const press = async (key: string, target: EventTarget = document.body, init: KeyboardEventInit = {}) => {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init })
    target.dispatchEvent(event)
    await flushPromises()
    return event
  }

  it.each(['w', 'W'])('opens the workspace selector with %s and allows switching', async key => {
    const event = await press(key, document.body, { shiftKey: key === 'W' })
    expect(event.defaultPrevented).toBe(true)
    expect(wrapper.get('[role="combobox"]').attributes('aria-expanded')).toBe('true')
    const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find(el => el.textContent?.includes('Beta'))!
    expect(option).toBeDefined()
    option.click()
    await flushPromises()
    expect(store.workspaceId).toBe('beta')
  })

  it('leaves modified shortcuts and typing alone', async () => {
    for (const init of [{ metaKey: true }, { ctrlKey: true }, { altKey: true }]) {
      expect((await press('w', document.body, init)).defaultPrevented).toBe(false)
      expect(wrapper.get('[role="combobox"]').attributes('aria-expanded')).toBe('false')
    }

    for (const tag of ['input', 'textarea', 'div']) {
      const editor = document.createElement(tag)
      if (tag === 'div') editor.setAttribute('contenteditable', 'true')
      wrapper.element.appendChild(editor)
      editor.focus()
      expect((await press('w', editor)).defaultPrevented).toBe(false)
      expect(wrapper.get('[role="combobox"]').attributes('aria-expanded')).toBe('false')
      editor.remove()
    }
  })
})