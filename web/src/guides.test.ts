import { describe, expect, it } from 'vitest'
import { NO_VARS_NOTE, PROVIDER_GUIDES, RECIPES, STACK_BLURBS, TAB_HELP, VAR_HELP, guideFor, stackBlurb, type L10n } from './guides'
import { missingKeys, translate, type Key } from './i18n'
import { NAV, TAB_IDS, groupOf, itemOf, startTab } from './nav'

const both = (l: L10n) => [l.es, l.en]
const filled = (s: string) => s.trim().length > 0

describe('provider guides', () => {
  it('have unique ids', () => {
    const ids = PROVIDER_GUIDES.map((g) => g.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('say everything in both languages, with no empty text', () => {
    for (const g of PROVIDER_GUIDES) {
      for (const l of [g.blurb, g.what, g.useFor, ...g.watchOut, ...g.snippets.map((s) => s.title), ...(g.next ? [g.next.label] : [])]) {
        expect(both(l).every(filled), `${g.id}: an empty translation`).toBe(true)
        expect(l.es, `${g.id}: untranslated "${l.es}"`).not.toBe(l.en)
      }
      expect(g.endpoints.length, `${g.id} lists where it listens`).toBeGreaterThan(0)
      for (const e of g.endpoints) {
        expect(filled(e.value)).toBe(true)
        if (typeof e.label !== 'string') expect(both(e.label).every(filled)).toBe(true)
      }
      for (const s of g.snippets) expect(filled(s.code), `${g.id}: an empty snippet`).toBe(true)
    }
  })

  it('keep the card line short enough to fit a card', () => {
    for (const g of PROVIDER_GUIDES) both(g.blurb).forEach((b) => expect(b.length, g.id).toBeLessThanOrEqual(90))
  })

  it('never put a real secret in a snippet', () => {
    // A password assigned a variable ($NIMBO_...) or a placeholder (<...>) is fine; a literal value is not.
    const suspicious = [/AKIA[0-9A-Z]{16}/, /[A-Za-z0-9+/]{40,}={0,2}/, /password\s*=\s*(?![$<])\S{6,}/i]
    for (const g of PROVIDER_GUIDES) {
      for (const s of g.snippets) {
        for (const re of suspicious) expect(re.test(s.code), `${g.id}: "${s.title.en}" looks like it contains a secret`).toBe(false)
      }
    }
  })

  it('send people to a screen that exists', () => {
    for (const g of PROVIDER_GUIDES) if (g.next) expect(TAB_IDS).toContain(g.next.tab)
  })

  it('are found by id', () => {
    expect(guideFor('floci-aws')?.id).toBe('floci-aws')
    expect(guideFor('nope')).toBeUndefined()
  })
})

describe('recipes', () => {
  it('have unique ids and only use providers that have a guide', () => {
    expect(new Set(RECIPES.map((r) => r.id)).size).toBe(RECIPES.length)
    const known = new Set(PROVIDER_GUIDES.map((g) => g.id))
    for (const r of RECIPES) {
      expect(r.providers.length).toBeGreaterThan(0)
      for (const p of r.providers) expect(known.has(p), `${r.id} uses ${p}`).toBe(true)
      expect(new Set(r.providers).size, `${r.id} repeats a provider`).toBe(r.providers.length)
    }
  })

  it('are written in both languages and point at real screens', () => {
    for (const r of RECIPES) {
      for (const l of [r.title, r.summary, r.next.label, ...r.youGet]) expect(both(l).every(filled)).toBe(true)
      expect(TAB_IDS).toContain(r.next.tab)
      expect(r.youGet.length).toBeGreaterThan(0)
    }
  })
})

describe('screen help', () => {
  it('exists for every screen, with real content in both languages', () => {
    for (const id of TAB_IDS) {
      const help = TAB_HELP[id]
      expect(help, `no help for ${id}`).toBeDefined()
      expect(help.points.length).toBeGreaterThanOrEqual(2)
      help.points.forEach((p) => expect(both(p).every(filled)).toBe(true))
    }
  })
})

describe('stack blurbs', () => {
  it('translate the stacks that ship and fall back to the stack own text for others', () => {
    for (const l of Object.values(STACK_BLURBS)) expect(both(l).every(filled)).toBe(true)
    expect(stackBlurb('multicloud', 'es', 'x')).toBe(STACK_BLURBS.multicloud.es)
    expect(stackBlurb('multicloud', 'en', 'x')).toBe(STACK_BLURBS.multicloud.en)
    expect(stackBlurb('my-own-stack', 'es', 'mine')).toBe('mine')
  })
})

describe('navigation', () => {
  it('lists every screen exactly once', () => {
    const ids = NAV.flatMap((g) => g.items.map((i) => i.id))
    expect(new Set(ids).size).toBe(ids.length)
    expect(ids.length).toBe(12)
    expect(TAB_IDS).toEqual(ids)
  })

  it('has a translation for every label, purpose and group', () => {
    const keys: Key[] = NAV.flatMap((g) => [g.group, ...g.items.flatMap((i) => [i.label, i.purpose])])
    for (const lang of ['es', 'en'] as const) for (const k of keys) expect(translate(lang, k), `${lang}:${k}`).toBeTruthy()
  })

  it('finds the group and item of a screen', () => {
    expect(groupOf('chaos')).toBe('navBreak')
    expect(groupOf('guide')).toBe('navStart')
    expect(itemOf('kube').label).toBe('viewKube')
  })

  it('opens on the guide until something is running', () => {
    expect(startTab(false)).toBe('guide')
    expect(startTab(true)).toBe('infra')
  })
})

describe('translations', () => {
  it('have the same keys in Spanish and English', () => {
    expect(missingKeys()).toEqual({ es: [], en: [] })
  })
})

describe('emulator variables', () => {
  it('are all explained, in both languages, and named like the backend names them', () => {
    expect(Object.keys(VAR_HELP).length).toBeGreaterThanOrEqual(8)
    for (const [name, h] of Object.entries(VAR_HELP)) {
      expect(name).toMatch(/^NIMBO_[A-Z0-9_]+$/)
      expect([h.label.es, h.label.en, h.help.es, h.help.en].every(filled), name).toBe(true)
    }
    expect(both(NO_VARS_NOTE).every(filled)).toBe(true)
  })
})
