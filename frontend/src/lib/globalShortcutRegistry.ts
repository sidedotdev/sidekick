type ShortcutHandler = () => void

interface RegisteredHandler {
  created: Date | string
  handler: ShortcutHandler
}

const registry = new Map<string, RegisteredHandler>()

let listenerAttached = false

const isMac = typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0

function onGlobalKeyDown(event: KeyboardEvent) {
  const modKey = isMac ? event.metaKey : event.ctrlKey
  if (!modKey || event.key !== 'Enter') return

  if (registry.size === 0) return

  let latest: RegisteredHandler | null = null
  for (const entry of registry.values()) {
    if (!latest || new Date(entry.created).getTime() > new Date(latest.created).getTime()) {
      latest = entry
    }
  }

  if (latest) {
    event.preventDefault()
    latest.handler()
  }
}

export function registerGlobalShortcut(id: string, created: Date | string, handler: ShortcutHandler) {
  registry.set(id, { created, handler })
  if (!listenerAttached) {
    document.addEventListener('keydown', onGlobalKeyDown)
    listenerAttached = true
  }
}

export function unregisterGlobalShortcut(id: string) {
  registry.delete(id)
  if (registry.size === 0 && listenerAttached) {
    document.removeEventListener('keydown', onGlobalKeyDown)
    listenerAttached = false
  }
}