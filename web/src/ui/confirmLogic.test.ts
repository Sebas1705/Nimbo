import { describe, expect, it } from 'vitest'
import { confirmStep } from './confirmLogic'
import { message, pick } from './text'

describe('confirmStep', () => {
  it('asks on the first press and never runs by itself', () => {
    expect(confirmStep('idle', 'press')).toEqual({ state: 'asking', run: false })
    for (const e of ['confirm', 'cancel', 'timeout', 'blur'] as const) expect(confirmStep('idle', e)).toEqual({ state: 'idle', run: false })
  })

  it('runs only when confirmed', () => {
    expect(confirmStep('asking', 'confirm')).toEqual({ state: 'idle', run: true })
  })

  it('withdraws on cancel, timeout and blur without running', () => {
    for (const e of ['cancel', 'timeout', 'blur'] as const) expect(confirmStep('asking', e)).toEqual({ state: 'idle', run: false })
  })

  it('pressing the main button again keeps asking: a double click is not a confirmation', () => {
    expect(confirmStep('asking', 'press')).toEqual({ state: 'asking', run: false })
  })
})

describe('text helpers', () => {
  it('pick and message', () => {
    expect(pick('es', 'hola', 'hello')).toBe('hola')
    expect(pick('en', 'hola', 'hello')).toBe('hello')
    expect(message(new Error('boom'))).toBe('boom')
    expect(message('plain')).toBe('plain')
    expect(message(42)).toBe('42')
  })
})
