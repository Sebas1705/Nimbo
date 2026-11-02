import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DockerStatus, Provider } from './api'
import { extraPorts, primaryPort, recipeStatus, runningCount, stepStatus } from './guideLogic'
import { applyTheme, helpOpen, helpUnseen, loadTheme, nextTheme, readPref, setHelpOpen, writePref } from './prefs'

const prov = (id: string, state: Provider['state'], ports: number[] = [1000]): Provider => ({
  id,
  name: id,
  cloud: 'other',
  description: '',
  image: 'x',
  ports,
  state,
})
const docker = (available: boolean): DockerStatus => ({ available, version: '29' })

describe('recipeStatus', () => {
  const recipe = { providers: ['a', 'b', 'c'] }

  it('is idle when none of it runs, and lists what to start in order', () => {
    const s = recipeStatus(recipe, [prov('a', 'stopped'), prov('b', 'not_created'), prov('c', 'stopped')])
    expect(s).toEqual({ state: 'idle', running: 0, total: 3, missing: ['a', 'b', 'c'] })
  })

  it('is partial when some runs, and only asks for the rest', () => {
    const s = recipeStatus(recipe, [prov('a', 'running'), prov('b', 'stopped'), prov('c', 'running')])
    expect(s).toEqual({ state: 'partial', running: 2, total: 3, missing: ['b'] })
  })

  it('is up when all run', () => {
    expect(recipeStatus(recipe, ['a', 'b', 'c'].map((id) => prov(id, 'running'))).state).toBe('up')
  })

  it('treats a provider the backend does not list as missing instead of crashing', () => {
    const s = recipeStatus(recipe, [prov('a', 'running')])
    expect(s.missing).toEqual(['b', 'c'])
    expect(s.state).toBe('partial')
    expect(recipeStatus(recipe, []).state).toBe('idle')
  })
})

describe('ports', () => {
  it('shows the first port big and the rest as extras', () => {
    const p = prov('k3s', 'running', [6443, 30080, 30081])
    expect(primaryPort(p)).toBe(6443)
    expect(extraPorts(p)).toEqual([30080, 30081])
  })

  it('copes with a provider that publishes nothing', () => {
    expect(primaryPort({ ports: [] })).toBeNull()
    expect(extraPorts({ ports: [] })).toEqual([])
  })
})

describe('stepStatus', () => {
  it('starts at step 1 when Docker is not answering, even if the list says something runs', () => {
    const s = stepStatus(docker(false), [prov('a', 'running')])
    expect(s.done).toEqual([false, false, false, false, false])
    expect(s.current).toBe(0)
  })

  it('moves to step 2 once Docker answers and nothing runs', () => {
    const s = stepStatus(docker(true), [])
    expect(s.done.slice(0, 2)).toEqual([true, false])
    expect(s.current).toBe(1)
  })

  it('moves on to pointing your code once something runs', () => {
    const s = stepStatus(docker(true), [prov('a', 'running')])
    expect(s.done.slice(0, 2)).toEqual([true, true])
    expect(s.current).toBe(2)
  })

  it('is safe before the first answer from the backend', () => {
    expect(stepStatus(null, []).done[0]).toBe(false)
  })
})

describe('runningCount', () => {
  it('counts only running providers', () => {
    expect(runningCount([prov('a', 'running'), prov('b', 'stopped'), prov('c', 'running')])).toBe(2)
  })
})

describe('preferences', () => {
  afterEach(() => vi.unstubAllGlobals())

  const store = () => {
    const m = new Map<string, string>()
    vi.stubGlobal('localStorage', { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) })
    return m
  }

  it('cycle the theme auto, light, dark and back', () => {
    expect(nextTheme('auto')).toBe('light')
    expect(nextTheme('light')).toBe('dark')
    expect(nextTheme('dark')).toBe('auto')
  })

  it('load the saved theme and ignore anything else', () => {
    const m = store()
    expect(loadTheme()).toBe('auto')
    writePref('theme', 'dark')
    expect(loadTheme()).toBe('dark')
    m.set('nimbo.theme', 'purple')
    expect(loadTheme()).toBe('auto')
  })

  it('apply a theme by setting or removing the attribute', () => {
    const attrs: Record<string, string> = {}
    const root = { setAttribute: (k: string, v: string) => void (attrs[k] = v), removeAttribute: (k: string) => void delete attrs[k] }
    applyTheme('dark', root)
    expect(attrs['data-theme']).toBe('dark')
    applyTheme('auto', root)
    expect(attrs['data-theme']).toBeUndefined()
  })

  it('remember whether the help of each screen is open, folded until you open it', () => {
    store()
    expect(helpOpen('chaos')).toBe(false)
    setHelpOpen('chaos', true)
    expect(helpOpen('chaos')).toBe(true)
    expect(helpOpen('labs')).toBe(false)
    setHelpOpen('chaos', false)
    expect(helpOpen('chaos')).toBe(false)
  })

  it('shows a dot until you have opened or folded the note once', () => {
    store()
    expect(helpUnseen('traces')).toBe(true)
    setHelpOpen('traces', true)
    expect(helpUnseen('traces')).toBe(false)
    expect(helpOpen('traces')).toBe(true)
    setHelpOpen('traces', false)
    expect(helpOpen('traces')).toBe(false)
  })

  it('survive storage that throws', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    })
    expect(readPref('x')).toBeNull()
    expect(() => writePref('x', '1')).not.toThrow()
    expect(helpOpen('guide')).toBe(false)
    expect(loadTheme()).toBe('auto')
  })
})
