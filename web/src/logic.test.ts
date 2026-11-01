import { afterEach, describe, expect, it, vi } from 'vitest'
import { barStyle, chaosState, countRunning, coverage, failures, formatBytes, formatMicros, groupByCloud, labProgress, podTone, type ChaosTarget, type FitSuite, type Provider } from './api'
import { detectLang, translate } from './i18n'

const p = (id: string, cloud: Provider['cloud'], state: Provider['state']): Provider => ({
  id,
  name: id,
  cloud,
  description: '',
  image: 'x',
  ports: [1],
  state,
})

describe('groupByCloud', () => {
  it('orders clouds fixed and drops empty ones', () => {
    const groups = groupByCloud([p('a', 'other', 'running'), p('b', 'azure', 'stopped'), p('c', 'aws', 'running'), p('d', 'azure', 'running')])
    expect(groups.map(([c]) => c)).toEqual(['aws', 'azure', 'other'])
    expect(groups[1][1].map((x) => x.id)).toEqual(['b', 'd'])
  })
})

describe('countRunning', () => {
  it('counts only running', () => {
    expect(countRunning([p('a', 'aws', 'running'), p('b', 'aws', 'stopped'), p('c', 'gcp', 'not_created')])).toBe(1)
  })
})

describe('i18n', () => {
  it('detects spanish and falls back to english', () => {
    expect(detectLang({ language: 'es-ES' })).toBe('es')
    expect(detectLang({ language: 'fr-FR' })).toBe('en')
  })
  it('has the same keys in both languages', () => {
    expect(translate('es', 'start')).toBe('Iniciar')
    expect(translate('en', 'start')).toBe('Start')
  })
})

describe('failures', () => {
  it('lists only failed steps with their reason', () => {
    const action = {
      id: 's',
      action: 'up' as const,
      results: [
        { id: 'redis', ok: true },
        { id: 'minio', ok: false, error: 'port busy' },
        { id: 'azurite', ok: false },
      ],
    }
    expect(failures(action)).toEqual(['minio: port busy', 'azurite: failed'])
    expect(failures({ ...action, results: [{ id: 'redis', ok: true }] })).toEqual([])
  })
})

describe('formatBytes', () => {
  it('uses B, KB and MB with one decimal', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1023)).toBe('1023 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })
})

describe('chaosState', () => {
  const base: ChaosTarget = { id: 'x', name: 'x', upstreamPort: 1, proxyPort: 2, endpoint: '', active: true, enabled: true, toxics: [] }
  it('ranks outage over faults over healthy', () => {
    expect(chaosState(base)).toBe('healthy')
    expect(chaosState({ ...base, toxics: [{ name: 'latency', type: 'latency', toxicity: 1, attributes: {} }] })).toBe('faults')
    expect(chaosState({ ...base, enabled: false, toxics: [{ name: 'latency', type: 'latency', toxicity: 1, attributes: {} }] })).toBe('outage')
  })
  it('a never-proxied target is healthy even though its flag defaults to enabled', () => {
    expect(chaosState({ ...base, active: false, enabled: false })).toBe('healthy')
  })
})

describe('coverage', () => {
  const suite = (statuses: Array<'pass' | 'fail' | 'skipped'>): FitSuite => ({
    id: 'x',
    cloud: 'aws',
    name: 'x',
    status: 'partial',
    ms: 1,
    steps: statuses.map((status, i) => ({ name: `s${i}`, status, ms: 1 })),
  })
  it('is the share of passing steps over everything that ran', () => {
    expect(coverage([])).toBe(0)
    expect(coverage([suite(['pass', 'pass', 'pass', 'fail'])])).toBe(75)
    expect(coverage([suite(['pass']), suite(['fail', 'skipped'])])).toBe(33)
  })
})

describe('labProgress', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('survives a browser without storage', () => {
    expect(labProgress.load().size).toBe(0)
    expect(() => labProgress.save(new Set(['a']))).not.toThrow()
  })

  it('round-trips ids and ignores corrupt data', () => {
    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) })
    labProgress.save(new Set(['first-bucket', 'queue-it-up']))
    expect([...labProgress.load()].sort()).toEqual(['first-bucket', 'queue-it-up'])

    store.set('nimbo.labs.done', '{not json')
    expect(labProgress.load().size).toBe(0)
    store.set('nimbo.labs.done', JSON.stringify(['ok', 42, null, { a: 1 }]))
    expect([...labProgress.load()]).toEqual(['ok'])
  })
})

describe('formatMicros', () => {
  it('picks the unit by magnitude', () => {
    expect(formatMicros(45)).toBe('45 µs')
    expect(formatMicros(120_000)).toBe('120 ms')
    expect(formatMicros(999_999)).toBe('1000 ms')
    expect(formatMicros(1_500_000)).toBe('1.50 s')
  })
})

describe('podTone', () => {
  it('separates fine, transitional and broken pods', () => {
    expect(podTone('Running')).toBe('ok')
    expect(podTone('Completed')).toBe('ok')
    expect(podTone('ContainerCreating')).toBe('warn')
    expect(podTone('Terminating')).toBe('warn')
    expect(podTone('CrashLoopBackOff')).toBe('bad')
    expect(podTone('ImagePullBackOff')).toBe('bad')
    expect(podTone('Whatever')).toBe('bad')
  })
})

describe('barStyle', () => {
  it('maps offset and duration to percentages inside the track', () => {
    expect(barStyle({ offsetMicros: 0, durationMicros: 120_000 }, 120_000)).toEqual({ left: 0, width: 100 })
    const mid = barStyle({ offsetMicros: 60_000, durationMicros: 30_000 }, 120_000)
    expect(mid.left).toBe(50)
    expect(mid.width).toBe(25)
  })
  it('never overflows, keeps tiny spans visible and survives a zero total', () => {
    expect(barStyle({ offsetMicros: 100, durationMicros: 1 }, 1_000_000).width).toBe(0.8)
    const over = barStyle({ offsetMicros: 90_000, durationMicros: 90_000 }, 100_000)
    expect(over.left + over.width).toBeLessThanOrEqual(100)
    expect(barStyle({ offsetMicros: 5, durationMicros: 5 }, 0)).toEqual({ left: 0, width: 100 })
    expect(barStyle({ offsetMicros: -5, durationMicros: 10 }, 100).left).toBe(0)
  })
})
