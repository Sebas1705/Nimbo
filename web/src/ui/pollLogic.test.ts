import { describe, expect, it } from 'vitest'
import { TAB_IDS } from '../nav'
import { DOWN_MS, FAST_MS, SLOW_MS, pollInterval } from './pollLogic'

describe('pollInterval', () => {
  it('looks often at the screens that show what is running', () => {
    for (const tab of ['guide', 'infra', 'registry', 'inspector', 'kube', 'traces', 'chaos'] as const) {
      expect(pollInterval({ tab, dockerUp: true })).toBe(FAST_MS)
    }
  })

  it('looks less often at the others', () => {
    for (const tab of ['labs', 'fidelity', 'activity', 'settings', 'updates', 'offline'] as const) {
      expect(pollInterval({ tab, dockerUp: true })).toBe(SLOW_MS)
    }
  })

  it('looks rarely when Docker is down, wherever you are', () => {
    for (const tab of TAB_IDS) expect(pollInterval({ tab, dockerUp: false })).toBe(DOWN_MS)
  })

  it('has an answer for every screen and never polls faster than the fast rate', () => {
    for (const tab of TAB_IDS) expect(pollInterval({ tab, dockerUp: true })).toBeGreaterThanOrEqual(FAST_MS)
  })
})
