import { describe, expect, it } from 'vitest'
import { countRunning, failures, groupByCloud, type Provider } from './api'
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
