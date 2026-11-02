import type { TabId } from '../nav'

/** How often the app asks the backend what is running, depending on what the person is looking at. */
export const FAST_MS = 3000
export const SLOW_MS = 10_000
/** With Docker unreachable nothing changes except Docker coming back, so look less often. */
export const DOWN_MS = 8000

/** Screens whose content is the live state of the emulators. The others only need it for the counter in the side rail. */
const LIVE_SCREENS: ReadonlySet<TabId> = new Set<TabId>(['guide', 'infra', 'registry', 'inspector', 'kube', 'traces', 'chaos'])

export function pollInterval(o: { tab: TabId; dockerUp: boolean }): number {
  if (!o.dockerUp) return DOWN_MS
  return LIVE_SCREENS.has(o.tab) ? FAST_MS : SLOW_MS
}
