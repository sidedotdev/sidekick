import { getCurrentScope, onScopeDispose } from 'vue'

export const TOOLTIP_OPEN_DELAY_MS = 500
// Once any tooltip has been shown, others open instantly until this long after the last one closes,
// so scanning across neighbouring tooltips doesn't pay the open delay each time.
export const TOOLTIP_WARM_WINDOW_MS = 300

let openCount = 0
let warm = false
let coolDownTimer: ReturnType<typeof setTimeout> | undefined

function clearCoolDown() {
  if (coolDownTimer !== undefined) {
    clearTimeout(coolDownTimer)
    coolDownTimer = undefined
  }
}

function startCoolDown() {
  clearCoolDown()
  coolDownTimer = setTimeout(() => {
    coolDownTimer = undefined
    warm = false
  }, TOOLTIP_WARM_WINDOW_MS)
}

export interface TooltipTriggerOptions<T> {
  show: (target: T) => void
  hide: () => void
  // Grace period before `leave` hides, e.g. so moving between adjacent hover targets doesn't flicker.
  leaveDelayMs?: number
}

export interface TooltipTrigger<T> {
  hover: (target: T) => void
  focus: (target: T) => void
  leave: () => void
  dismiss: () => void
}

export function useTooltipTrigger<T = void>({ show, hide, leaveDelayMs = 0 }: TooltipTriggerOptions<T>): TooltipTrigger<T> {
  let isOpen = false
  let openTimer: ReturnType<typeof setTimeout> | undefined
  let leaveTimer: ReturnType<typeof setTimeout> | undefined

  function cancelOpen() {
    if (openTimer !== undefined) {
      clearTimeout(openTimer)
      openTimer = undefined
    }
  }

  function cancelLeave() {
    if (leaveTimer !== undefined) {
      clearTimeout(leaveTimer)
      leaveTimer = undefined
    }
  }

  function reveal(target: T) {
    cancelOpen()
    cancelLeave()
    if (!isOpen) {
      isOpen = true
      openCount++
      warm = true
      clearCoolDown()
    }
    show(target)
  }

  function hover(target: T) {
    cancelLeave()
    if (isOpen || warm) {
      reveal(target)
      return
    }
    cancelOpen()
    openTimer = setTimeout(() => reveal(target), TOOLTIP_OPEN_DELAY_MS)
  }

  function dismiss() {
    cancelOpen()
    cancelLeave()
    if (!isOpen) return
    isOpen = false
    openCount--
    if (openCount === 0) startCoolDown()
    hide()
  }

  function leave() {
    cancelOpen()
    if (!isOpen) return
    if (leaveDelayMs <= 0) {
      dismiss()
      return
    }
    cancelLeave()
    leaveTimer = setTimeout(dismiss, leaveDelayMs)
  }

  if (getCurrentScope()) onScopeDispose(dismiss)

  return { hover, focus: reveal, leave, dismiss }
}