import type { InjectionKey } from 'vue'
import type { FlowAction } from './models'

/** Notified after a user request is submitted via its keyboard shortcut without Shift held. */
export type UserRequestShortcutSubmittedHandler = (flowAction: FlowAction) => void

export const USER_REQUEST_SHORTCUT_SUBMITTED_KEY: InjectionKey<UserRequestShortcutSubmittedHandler> =
  Symbol('userRequestShortcutSubmitted')

export const isUserRequestAction = (action: FlowAction): boolean =>
  action.actionType === 'user_request' || action.actionType.startsWith('user_request.')