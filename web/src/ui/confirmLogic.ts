/**
 * The two-step confirmation of a risky button, as a tiny state machine so it can be tested without a browser:
 * the first press asks, the second press (or the explicit confirm) acts, anything else or the passing of time cancels.
 */
export type ConfirmState = 'idle' | 'asking'

export type ConfirmEvent = 'press' | 'confirm' | 'cancel' | 'timeout' | 'blur'

export interface ConfirmStep {
  state: ConfirmState
  /** True exactly when the action must run now. */
  run: boolean
}

export function confirmStep(state: ConfirmState, event: ConfirmEvent): ConfirmStep {
  if (state === 'idle') return { state: event === 'press' ? 'asking' : 'idle', run: false }
  // asking
  if (event === 'confirm') return { state: 'idle', run: true }
  if (event === 'press') return { state: 'asking', run: false }
  return { state: 'idle', run: false }
}

/** How long the question stays before it withdraws itself. */
export const CONFIRM_MS = 7000
