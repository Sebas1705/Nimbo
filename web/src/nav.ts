import type { Key } from './i18n'

/** Every screen of the app. */
export type TabId = 'guide' | 'infra' | 'inspector' | 'kube' | 'traces' | 'chaos' | 'fidelity' | 'labs'

export interface NavItem {
  id: TabId
  label: Key
  /** One line under the page title: what this screen is for. */
  purpose: Key
}

export interface NavGroup {
  /** The stage of the work, in the order people actually go through it. */
  group: Key
  items: NavItem[]
}

/**
 * The rail is organised by what you are doing, not by feature: get oriented, bring things up, look inside them,
 * break them on purpose, and learn from the result.
 */
export const NAV: NavGroup[] = [
  { group: 'navStart', items: [{ id: 'guide', label: 'viewGuide', purpose: 'purposeGuide' }] },
  { group: 'navBuild', items: [{ id: 'infra', label: 'viewInfra', purpose: 'purposeInfra' }] },
  {
    group: 'navObserve',
    items: [
      { id: 'inspector', label: 'viewInspector', purpose: 'purposeInspector' },
      { id: 'kube', label: 'viewKube', purpose: 'purposeKube' },
      { id: 'traces', label: 'viewTraces', purpose: 'purposeTraces' },
    ],
  },
  { group: 'navBreak', items: [{ id: 'chaos', label: 'viewChaos', purpose: 'purposeChaos' }] },
  {
    group: 'navLearn',
    items: [
      { id: 'labs', label: 'viewLabs', purpose: 'purposeLabs' },
      { id: 'fidelity', label: 'viewFidelity', purpose: 'purposeFidelity' },
    ],
  },
]

export const TAB_IDS: TabId[] = NAV.flatMap((g) => g.items.map((i) => i.id))

/** Which group a screen belongs to, for the small label above its title. */
export function groupOf(tab: TabId): Key {
  return NAV.find((g) => g.items.some((i) => i.id === tab))!.group
}

export function itemOf(tab: TabId): NavItem {
  return NAV.flatMap((g) => g.items).find((i) => i.id === tab)!
}

/** The tab the app opens on: the guide until something is running, then the place where you manage it. */
export function startTab(anythingRunning: boolean): TabId {
  return anythingRunning ? 'infra' : 'guide'
}
