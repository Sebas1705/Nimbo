import { describe, expect, it } from 'vitest'
import type { ComposeDef, SourceDef } from './api'
import {
  changedFiles,
  composeBody,
  composeSummary,
  composeTone,
  composeToDraft,
  emptyComposeDraft,
  emptyImageDraft,
  emptySourceDraft,
  formatCommand,
  imageBody,
  isAbsolutePath,
  isGitUrl,
  missingFiles,
  parseCommand,
  secretName,
  shortPath,
  sourceBody,
  sourceDetail,
  sourceStatusLabel,
  sourceTone,
  syncLabel,
  upBlocker,
  validateCompose,
  validateFileRef,
  validateId,
  validateImage,
  validatePorts,
  validateSource,
  type ImageDraft,
} from './registryLogic'

const ctx = { takenIds: ['floci-aws', 'redis'], usedPorts: { '4566': 'Floci AWS' } }
const okImage = (patch: Partial<ImageDraft> = {}): ImageDraft => ({ ...emptyImageDraft(), id: 'my-api', name: 'My API', image: 'ghcr.io/me/api:1.2', ports: ['8081'], ...patch })

describe('ids', () => {
  it('follow the server rule', () => {
    expect(validateId('my-api', [], 'en')).toBe('')
    expect(validateId('', [], 'en')).not.toBe('')
    for (const bad of ['My-Api', '-api', 'a_b', 'a'.repeat(32), 'a b']) expect(validateId(bad, [], 'en'), bad).not.toBe('')
    expect(validateId('a'.repeat(31), [], 'en')).toBe('')
  })
  it('refuse a taken id', () => {
    expect(validateId('redis', ['redis'], 'es')).toMatch(/Ya existe/)
    expect(validateId('redis', ['redis'], 'en')).toMatch(/already exists/)
  })
  it('names the secret like the backend', () => {
    expect(secretName('my-api', 'api_key')).toBe('NIMBO_C_MY_API_API_KEY')
  })
})

describe('ports', () => {
  it('accept 1024..65535 and reject the rest', () => {
    expect(validatePorts(['1024', '65535'], {}, 'en')).toBe('')
    for (const bad of ['1023', '65536', 'abc', '80.5', '']) expect(validatePorts([bad], {}, 'en'), bad).not.toBe('')
  })
  it('reject duplicates, more than ten, and ports somebody else uses', () => {
    expect(validatePorts(['8081', '8081'], {}, 'en')).toMatch(/repeated/)
    expect(validatePorts(Array.from({ length: 11 }, (_, i) => String(9000 + i)), {}, 'en')).toMatch(/At most 10/)
    expect(validatePorts(['4566'], { '4566': 'Floci AWS' }, 'en')).toMatch(/Floci AWS/)
  })
})

describe('commands', () => {
  it('split on spaces and group with quotes', () => {
    expect(parseCommand('serve --dev').args).toEqual(['serve', '--dev'])
    expect(parseCommand(`run "a b" 'c d' e`).args).toEqual(['run', 'a b', 'c d', 'e'])
    expect(parseCommand('').args).toEqual([])
    expect(parseCommand('x ""').args).toEqual(['x', ''])
  })
  it('report an unclosed quote', () => {
    expect(parseCommand('run "oops').error).toBe('quote')
  })
  it('round-trip through formatCommand', () => {
    for (const args of [['serve', '--dev'], ['sh', '-c', 'echo hi there'], ['a', '']]) expect(parseCommand(formatCommand(args)).args).toEqual(args)
  })
})

describe('own image validation', () => {
  it('accepts a complete image', () => {
    expect(validateImage(okImage(), ctx, 'en')).toEqual({})
  })
  it('requires name and image and checks the image name', () => {
    expect(validateImage(okImage({ name: ' ' }), ctx, 'en').name).toBeTruthy()
    expect(validateImage(okImage({ image: '' }), ctx, 'en').image).toBeTruthy()
    expect(validateImage(okImage({ image: 'My Image' }), ctx, 'en').image).toBeTruthy()
    expect(validateImage(okImage({ name: 'x'.repeat(61) }), ctx, 'en').name).toBeTruthy()
  })
  it('does not ask for the id of an image being edited, but does for a new one', () => {
    expect(validateImage(okImage({ id: 'Bad', isNew: false }), ctx, 'en').id).toBeUndefined()
    expect(validateImage(okImage({ id: 'redis' }), ctx, 'en').id).toBeTruthy()
  })
  it('checks environment keys, values and uniqueness across variables and secrets', () => {
    expect(validateImage(okImage({ env: [{ key: '1BAD', value: 'x' }] }), ctx, 'en').env).toBeTruthy()
    expect(validateImage(okImage({ env: [{ key: 'MODE', value: 'a\u0001b' }] }), ctx, 'en').env).toBeTruthy()
    expect(validateImage(okImage({ env: [{ key: 'MODE', value: 'x'.repeat(501) }] }), ctx, 'en').env).toBeTruthy()
    expect(validateImage(okImage({ env: [{ key: 'MODE', value: 'a' }, { key: 'MODE', value: 'b' }] }), ctx, 'en').env).toMatch(/repeated/)
    expect(validateImage(okImage({ env: [{ key: 'KEY', value: 'a' }], secretKeys: ['KEY'] }), ctx, 'en').secretKeys).toMatch(/repeated/)
    expect(validateImage(okImage({ secretKeys: ['has space'] }), ctx, 'en').secretKeys).toBeTruthy()
    expect(validateImage(okImage({ env: Array.from({ length: 31 }, (_, i) => ({ key: `K${i}`, value: '' })) }), ctx, 'en').env).toMatch(/At most 30/)
  })
  it('ignores blank environment rows', () => {
    expect(validateImage(okImage({ env: [{ key: '', value: '' }] }), ctx, 'en')).toEqual({})
    expect(imageBody(okImage({ env: [{ key: '', value: '' }, { key: ' MODE ', value: 'test' }] })).env).toEqual([{ key: 'MODE', value: 'test' }])
  })
  it('checks command and volume', () => {
    expect(validateImage(okImage({ command: 'a "b' }), ctx, 'en').command).toBeTruthy()
    expect(validateImage(okImage({ command: Array.from({ length: 21 }, () => 'x').join(' ') }), ctx, 'en').command).toMatch(/At most 20/)
    expect(validateImage(okImage({ command: 'x'.repeat(201) }), ctx, 'en').command).toBeTruthy()
    expect(validateImage(okImage({ volume: 'data' }), ctx, 'en').volume).toBeTruthy()
    expect(validateImage(okImage({ volume: '/data' }), ctx, 'en').volume).toBeUndefined()
    expect(validateImage(okImage({ notes: 'n'.repeat(501) }), ctx, 'en').notes).toBeTruthy()
  })
  it('builds the request body the server expects', () => {
    const body = imageBody(okImage({ command: 'serve --dev', volume: '/data', secretKeys: ['API_KEY'], notes: ' hi ' }))
    expect(body).toEqual({ name: 'My API', image: 'ghcr.io/me/api:1.2', ports: [8081], env: [], secretKeys: ['API_KEY'], command: ['serve', '--dev'], volume: '/data', notes: 'hi' })
    expect(imageBody(okImage()).volume).toBeNull()
    expect(imageBody(okImage()).command).toEqual([])
  })
})

describe('sources', () => {
  it('tell absolute paths from relative ones', () => {
    for (const p of ['C:\\code\\repo', 'c:/code/repo', '\\\\srv\\share', '/home/me/repo']) expect(isAbsolutePath(p), p).toBe(true)
    for (const p of ['repo', './repo', 'deploy/compose.yml']) expect(isAbsolutePath(p), p).toBe(false)
  })
  it('allow only the Git addresses the server allows', () => {
    for (const u of ['https://github.com/me/repo.git', 'https://host/path', 'ssh://git@host/me/repo', 'git@github.com:me/repo.git']) expect(isGitUrl(u), u).toBe(true)
    for (const u of ['file:///tmp/x', 'ext::sh -c x', '-oProxy=x', 'http://github.com/me/repo', 'https://host/a b', 'git@host', 'ssh://root@host/x', '']) expect(isGitUrl(u), u).toBe(false)
  })
  it('validate a local folder and a Git source', () => {
    expect(validateSource({ ...emptySourceDraft(), id: 'r', path: 'C:\\code\\r' }, [], 'en')).toEqual({})
    expect(validateSource({ ...emptySourceDraft(), id: 'r', path: 'relative' }, [], 'en').path).toBeTruthy()
    expect(validateSource({ ...emptySourceDraft(), id: 'r', path: '' }, [], 'en').path).toBeTruthy()
    const git = { ...emptySourceDraft(), id: 'g', kind: 'git' as const }
    expect(validateSource({ ...git, url: 'https://github.com/me/r.git' }, [], 'en')).toEqual({})
    expect(validateSource({ ...git, url: 'file:///x' }, [], 'en').url).toBeTruthy()
    expect(validateSource({ ...git, url: 'https://h/r', branch: '../x' }, [], 'en').branch).toBeTruthy()
    expect(validateSource({ ...git, url: 'https://h/r', branch: 'feature/x' }, [], 'en').branch).toBeUndefined()
    expect(validateSource({ ...git, id: 'taken', url: 'https://h/r' }, ['taken'], 'en').id).toBeTruthy()
  })
  it('build the body for each kind', () => {
    expect(sourceBody({ ...emptySourceDraft(), path: ' C:\\r ' })).toEqual({ kind: 'local', path: 'C:\\r' })
    expect(sourceBody({ ...emptySourceDraft(), kind: 'git', url: 'https://h/r', branch: '' })).toEqual({ kind: 'git', url: 'https://h/r', branch: null })
  })
  const src = (patch: Partial<SourceDef>): SourceDef => ({ id: 's', kind: 'git', path: null, url: 'https://h/r', branch: null, dir: '/d', status: 'ok', head: 'abcdef1234', currentBranch: 'main', dirty: false, behind: 0, syncedAt: 1, error: null, ...patch })
  it('word the status plainly', () => {
    expect(sourceStatusLabel('not-cloned', 'en')).toBe('Not cloned')
    expect(sourceStatusLabel('missing', 'es')).toBe('No encontrada')
    expect(sourceTone('ok')).toBe('ok')
    expect(sourceTone('error')).toBe('bad')
    expect(sourceTone('not-cloned')).toBe('warn')
  })
  it('summarise branch, short head, behind and dirty', () => {
    expect(sourceDetail(src({}), 'en')).toBe('main · abcdef1')
    expect(sourceDetail(src({ behind: 2, dirty: true }), 'en')).toBe('main · abcdef1 · 2 new changes to pull · with uncommitted changes')
    expect(sourceDetail(src({ behind: 1 }), 'es')).toContain('1 cambio nuevo')
    expect(sourceDetail(src({ head: null, currentBranch: null, behind: null }), 'en')).toBe('')
  })
  it('name the sync button by what it will do', () => {
    expect(syncLabel(src({ status: 'not-cloned' }), 'en')).toBe('Clone')
    expect(syncLabel(src({}), 'es')).toBe('Sincronizar')
    expect(syncLabel(src({ kind: 'local' }), 'en')).toBe('Refresh status')
  })
})

describe('compose files', () => {
  it('need a path that matches where it is looked up', () => {
    expect(validateFileRef({ source: '', path: 'C:\\x\\compose.yml' }, 'compose', 'en')).toBe('')
    expect(validateFileRef({ source: '', path: 'compose.yml' }, 'compose', 'en')).toMatch(/complete/)
    expect(validateFileRef({ source: 'r', path: 'deploy/compose.yaml' }, 'compose', 'en')).toBe('')
    expect(validateFileRef({ source: 'r', path: '/etc/compose.yml' }, 'compose', 'en')).toMatch(/relative/)
    expect(validateFileRef({ source: 'r', path: '../x/compose.yml' }, 'compose', 'en')).toMatch(/cannot leave/)
    expect(validateFileRef({ source: 'r', path: 'a\\..\\b.yml' }, 'compose', 'en')).toMatch(/cannot leave/)
    expect(validateFileRef({ source: 'r', path: 'compose.txt' }, 'compose', 'en')).toMatch(/\.yml/)
    expect(validateFileRef({ source: 'r', path: '' }, 'compose', 'en')).toBeTruthy()
  })
  it('recognise env file names', () => {
    for (const p of ['.env', '.env.test', 'ci.env', 'dir/.env.local']) expect(validateFileRef({ source: 'r', path: p }, 'env', 'en'), p).toBe('')
    for (const p of ['env.txt', 'envfile', 'compose.yml']) expect(validateFileRef({ source: 'r', path: p }, 'env', 'en'), p).toBeTruthy()
  })
})

describe('compose profile validation', () => {
  const draft = () => ({ ...emptyComposeDraft(), id: 'api-tests', name: 'API tests', files: [{ source: 'r', path: 'deploy/c.yml' }] })
  it('accepts a complete profile', () => {
    expect(validateCompose(draft(), [], ['r'], 'en')).toEqual({})
  })
  it('flags each bad part where it is', () => {
    const e = validateCompose({ ...draft(), id: 'Bad', name: '', files: [{ source: 'gone', path: 'x.yml' }, { source: '', path: 'rel.yml' }], profiles: ['ok', 'no good'], envFile: { source: 'r', path: 'notenv.txt' } }, [], ['r'], 'en')
    expect(Object.keys(e).sort()).toEqual(['env', 'file0', 'file1', 'id', 'name', 'profiles'])
  })
  it('requires at least one file and ignores an empty env file', () => {
    expect(validateCompose({ ...draft(), files: [] }, [], ['r'], 'en').files).toBeTruthy()
    expect(validateCompose({ ...draft(), envFile: { source: 'r', path: '' } }, [], ['r'], 'en')).toEqual({})
  })
  it('builds the body, with null for no env file', () => {
    expect(composeBody({ ...draft(), purpose: ' tests ', profiles: ['tests'] })).toEqual({ name: 'API tests', purpose: 'tests', files: [{ source: 'r', path: 'deploy/c.yml' }], profiles: ['tests'], envFile: null })
    expect(composeBody({ ...draft(), envFile: { source: 'r', path: '.env.test' } }).envFile).toEqual({ source: 'r', path: '.env.test' })
  })
})

describe('compose state', () => {
  const def = (patch: Partial<ComposeDef> = {}): ComposeDef => ({
    id: 'c', name: 'C', purpose: '', files: [{ source: 'r', path: 'c.yml' }], profiles: [], envFile: null,
    state: { running: 0, total: 0, services: [] },
    fileStatus: [{ path: '/r/c.yml', status: 'ok', sha256: 'x' }], envFileStatus: null, lastUpSha: null, lastUpAt: null,
    ...patch,
  })
  it('summarise how much is running', () => {
    expect(composeSummary(def(), 'en')).toBe('Stopped')
    expect(composeSummary(def({ state: { running: 0, total: 3, services: [] } }), 'en')).toBe('Stopped · 3 services')
    expect(composeSummary(def({ state: { running: 2, total: 3, services: [] } }), 'es')).toBe('2 de 3 en marcha')
    expect(composeTone(def({ state: { running: 3, total: 3, services: [] } }))).toBe('ok')
    expect(composeTone(def({ state: { running: 1, total: 3, services: [] } }))).toBe('warn')
    expect(composeTone(def())).toBe('idle')
  })
  it('lists missing and changed files, env file included', () => {
    const d = def({ fileStatus: [{ path: '/a.yml', status: 'changed', sha256: 'x' }, { path: '/b.yml', status: 'missing', sha256: null }], envFileStatus: { path: '/.env', status: 'changed', sha256: 'y' } })
    expect(changedFiles(d)).toEqual(['/a.yml', '/.env'])
    expect(missingFiles(d)).toEqual(['/b.yml'])
  })
  it('explains why Up is blocked', () => {
    expect(upBlocker(def(), true, 'en')).toBe('')
    expect(upBlocker(def(), false, 'en')).toMatch(/Docker/)
    expect(upBlocker(def({ fileStatus: [{ path: '/b.yml', status: 'missing', sha256: null }] }), true, 'en')).toContain('/b.yml')
  })
  it('turns a saved profile back into a draft that validates', () => {
    const d = composeToDraft(def({ envFile: { source: 'r', path: '.env' } }))
    expect(d.isNew).toBe(false)
    expect(d.envFile.path).toBe('.env')
    expect(composeToDraft(def({ files: [] })).files).toHaveLength(1)
  })
  it('shortens long paths', () => {
    expect(shortPath('C:\\code\\repo\\deploy\\c.yml')).toBe('…/deploy/c.yml')
    expect(shortPath('c.yml')).toBe('c.yml')
  })
})
