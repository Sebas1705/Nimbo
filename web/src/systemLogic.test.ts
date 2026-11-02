import { describe, expect, it } from 'vitest'
import type { UpdateFile, UpdateStatus } from './api'
import { TAB_IDS } from './nav'
import { ago, backupName, describeEntry, formatUptime, idsToImport, installerFor, maskSecret, parseBackup, updateDot, usageRanking } from './systemLogic'

const file = (version: string, name: string, verified: boolean): UpdateFile => ({ version, name, sha256: 'a', verified, source: 'github', size: 1, at: 0 })

describe('formatUptime', () => {
  it('uses the largest sensible unit', () => {
    expect(formatUptime(0)).toBe('0 s')
    expect(formatUptime(45)).toBe('45 s')
    expect(formatUptime(60)).toBe('1 min')
    expect(formatUptime(125 * 60)).toBe('2 h 05 min')
    expect(formatUptime((24 + 4) * 3600)).toBe('1 d 4 h')
  })
})

describe('ago', () => {
  const now = Date.UTC(2026, 10, 2, 12, 0, 0)
  it('speaks in both languages', () => {
    expect(ago(now - 10_000, now, 'en')).toBe('just now')
    expect(ago(now - 10_000, now, 'es')).toBe('ahora mismo')
    expect(ago(now - 5 * 60_000, now, 'en')).toBe('5 min ago')
    expect(ago(now - 5 * 60_000, now, 'es')).toBe('hace 5 min')
    expect(ago(now - 3 * 3600_000, now, 'en')).toBe('3 h ago')
    expect(ago(now - 26 * 3600_000, now, 'es')).toBe('ayer')
    expect(ago(now - 3 * 86400_000, now, 'en')).toBe('3 days ago')
  })
  it('never goes negative when the clocks disagree', () => {
    expect(ago(now + 60_000, now, 'en')).toBe('just now')
  })
})

describe('usageRanking', () => {
  it('orders by time up and hides what was never used', () => {
    const r = usageRanking({
      a: { starts: 1, stops: 0, upSeconds: 10, lastStartedAt: null },
      b: { starts: 3, stops: 1, upSeconds: 500, lastStartedAt: null },
      c: { starts: 0, stops: 0, upSeconds: 0, lastStartedAt: null },
    })
    expect(r.map(([id]) => id)).toEqual(['b', 'a'])
  })
})

describe('describeEntry', () => {
  it('describes known kinds in both languages and shows unknown ones as they are', () => {
    expect(describeEntry({ at: 0, kind: 'provider.start', subject: 'redis' }, 'es')).toBe('Arrancó redis')
    expect(describeEntry({ at: 0, kind: 'provider.start', subject: 'redis' }, 'en')).toBe('Started redis')
    expect(describeEntry({ at: 0, kind: 'provider.persist', subject: 'redis', detail: 'on' }, 'en')).toContain('on')
    expect(describeEntry({ at: 0, kind: 'something.new', subject: 'x' }, 'en')).toBe('something.new x')
  })
  it('covers every kind the backend records', () => {
    const kinds = [
      'provider.start', 'provider.stop', 'provider.remove', 'provider.persist', 'provider.data.delete', 'stack.up', 'stack.down', 'lab.verify',
      'lab.complete', 'fidelity.run', 'export', 'secret.set', 'secret.delete', 'update.check', 'update.download', 'update.import',
      'secret.reveal', 'variables.init', 'variables.apply', 'update.install', 'images.load', 'images.pull', 'settings.change', 'data.import',
    ]
    for (const k of kinds) {
      for (const lang of ['es', 'en'] as const) expect(describeEntry({ at: 0, kind: k, subject: 's' }, lang)).not.toBe(`${k} s`)
    }
  })
})

describe('updates', () => {
  it('shows the dot only when something newer exists', () => {
    expect(updateDot(null)).toBe(false)
    expect(updateDot({ available: false } as UpdateStatus)).toBe(false)
    expect(updateDot({ available: true } as UpdateStatus)).toBe(true)
  })
  it('prefers a verified installer for the version', () => {
    const files = [file('0.3.0', 'a.msi', false), file('0.3.0', 'b.msi', true), file('0.2.0', 'c.msi', true), file('0.3.0', 'x.jar', true)]
    expect(installerFor(files, '0.3.0')?.name).toBe('b.msi')
    expect(installerFor([file('0.3.0', 'a.msi', false)], '0.3.0')?.name).toBe('a.msi')
    expect(installerFor(files, '9.9.9')).toBeNull()
    expect(installerFor(files, undefined)).toBeNull()
  })
})

describe('secrets', () => {
  it('never reveals anything', () => {
    expect(maskSecret(true)).toMatch(/^•+$/)
    expect(maskSecret(false)).toBe('')
  })
})

describe('backups', () => {
  it('accepts what the server wrote and rejects anything else', () => {
    expect(parseBackup('{"format":1,"exportedAt":1,"files":{}}').ok).toBe(true)
    expect(parseBackup('not json').ok).toBe(false)
    expect(parseBackup('[1,2]').ok).toBe(false)
    expect(parseBackup('{"format":"1","files":{}}').ok).toBe(false)
    expect(parseBackup('null').ok).toBe(false)
  })
  it('names the file after the day', () => {
    expect(backupName(new Date('2026-11-02T10:00:00Z'))).toBe('nimbo-backup-2026-11-02.json')
  })
})

describe('idsToImport', () => {
  it('sends only well-formed ids the server does not have', () => {
    expect(idsToImport(['a', 'b', '../x', 'UP', ''], { a: {} })).toEqual(['b'])
    expect(idsToImport([], {})).toEqual([])
  })
})

describe('the system screens are in the navigation', () => {
  it('has the four screens', () => {
    for (const id of ['activity', 'settings', 'updates', 'offline']) expect(TAB_IDS).toContain(id)
  })
})
