import { describe, expect, it } from 'vitest'
import { chaosState, countRunning, failures, formatBytes, groupByCloud, type ChaosTarget, type Provider } from './api'
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
