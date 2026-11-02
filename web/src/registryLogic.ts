import type { ComposeDef, ComposeFileStatus, ComposeFileRef, SourceDef } from './api'
import type { Lang } from './i18n'
import type { Tone } from './ui/text'

/**
 * Pure rules of the Registry screen. The validation mirrors what the server checks (see REGISTRY.md), so people see the
 * problem next to the field instead of after pressing Save; the server stays the judge and its message is shown too.
 */

const m = (lang: Lang, es: string, en: string) => (lang === 'es' ? es : en)

export const ID_RE = /^[a-z0-9][a-z0-9-]{0,30}$/
export const IMAGE_RE = /^[a-z0-9][a-z0-9._/:@-]{0,200}$/
export const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/
export const VOLUME_RE = /^\/[A-Za-z0-9._/-]{0,200}$/
const PROFILE_RE = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/
const BRANCH_RE = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$/
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u001f\u007f]/

export const LIMITS = { name: 60, notes: 500, envValue: 500, envEntries: 30, ports: 10, commandItems: 20, commandItem: 200, purpose: 200 }

export type Errors = Record<string, string>

// ---- ids, keys, ports ---------------------------------------------------------------------------------------------------

/** Error for an id, or '' when it is fine. `taken` are the ids already used in the same list or by built-in emulators. */
export function validateId(id: string, taken: string[], lang: Lang): string {
  if (!id) return m(lang, 'Escribe un identificador.', 'Enter an identifier.')
  if (!ID_RE.test(id)) return m(lang, 'Solo minúsculas, números y guiones; empieza por letra o número; máximo 31 caracteres.', 'Only lowercase letters, digits and hyphens; start with a letter or digit; at most 31 characters.')
  if (taken.includes(id)) return m(lang, 'Ya existe algo con ese identificador.', 'Something with that identifier already exists.')
  return ''
}

export function validateKey(key: string, lang: Lang): string {
  if (!KEY_RE.test(key)) return m(lang, `"${key}" no es un nombre válido: letras, números y _, sin empezar por número (máx. 64).`, `"${key}" is not a valid name: letters, digits and _, not starting with a digit (max 64).`)
  return ''
}

/** The name the secret gets in Settings > Emulator variables: NIMBO_C_<ID>_<KEY>. */
export function secretName(id: string, key: string): string {
  return `NIMBO_C_${id.toUpperCase().replace(/-/g, '_')}_${key.toUpperCase()}`
}

/** Parses a port typed by hand; null when it is not a whole number. */
export function parsePort(s: string): number | null {
  return /^\d{1,5}$/.test(s.trim()) ? Number(s.trim()) : null
}

export function validatePorts(ports: string[], usedBy: Record<string, string>, lang: Lang): string {
  if (ports.length > LIMITS.ports) return m(lang, `Como máximo ${LIMITS.ports} puertos.`, `At most ${LIMITS.ports} ports.`)
  const seen = new Set<number>()
  for (const raw of ports) {
    const n = parsePort(raw)
    if (n === null || n < 1024 || n > 65535) return m(lang, `El puerto "${raw}" debe estar entre 1024 y 65535.`, `Port "${raw}" must be between 1024 and 65535.`)
    if (seen.has(n)) return m(lang, `El puerto ${n} está repetido.`, `Port ${n} is repeated.`)
    seen.add(n)
    const owner = usedBy[String(n)]
    if (owner) return m(lang, `El puerto ${n} ya lo usa ${owner}.`, `Port ${n} is already used by ${owner}.`)
  }
  return ''
}

// ---- command line -----------------------------------------------------------------------------------------------------

/** Splits "serve --dev 'a b'" into arguments. Quotes group words; nothing else is interpreted (no shell is involved). */
export function parseCommand(text: string): { args: string[]; error: 'quote' | null } {
  const args: string[] = []
  let cur = ''
  let has = false
  let quote: string | null = null
  for (const ch of text) {
    if (quote) {
      if (ch === quote) quote = null
      else cur += ch
    } else if (ch === '"' || ch === "'") {
      quote = ch
      has = true
    } else if (/\s/.test(ch)) {
      if (has || cur) args.push(cur)
      cur = ''
      has = false
    } else {
      cur += ch
    }
  }
  if (quote) return { args, error: 'quote' }
  if (has || cur) args.push(cur)
  return { args, error: null }
}

/** The inverse, for showing a saved command in the form: arguments with spaces get quotes. */
export function formatCommand(args: string[]): string {
  return args.map((a) => (a === '' || /[\s"']/.test(a) ? (a.includes('"') ? `'${a}'` : `"${a}"`) : a)).join(' ')
}

// ---- own images -------------------------------------------------------------------------------------------------------

export interface ImageDraft {
  id: string
  name: string
  image: string
  ports: string[]
  env: Array<{ key: string; value: string }>
  secretKeys: string[]
  command: string
  volume: string
  notes: string
  isNew: boolean
}

export function emptyImageDraft(): ImageDraft {
  return { id: '', name: '', image: '', ports: [], env: [], secretKeys: [], command: '', volume: '', notes: '', isNew: true }
}

export interface ImageContext {
  /** Ids of built-in emulators and of the other own images. */
  takenIds: string[]
  /** Port (as text) -> who already listens there, for everything except the image being edited. */
  usedPorts: Record<string, string>
}

export function validateImage(d: ImageDraft, ctx: ImageContext, lang: Lang): Errors {
  const e: Errors = {}
  if (d.isNew) {
    const idErr = validateId(d.id, ctx.takenIds, lang)
    if (idErr) e.id = idErr
  }
  if (!d.name.trim()) e.name = m(lang, 'Ponle un nombre para reconocerla.', 'Give it a name so you can recognise it.')
  else if (d.name.trim().length > LIMITS.name) e.name = m(lang, `Máximo ${LIMITS.name} caracteres.`, `At most ${LIMITS.name} characters.`)

  if (!d.image.trim()) e.image = m(lang, 'Indica la imagen de Docker, por ejemplo ghcr.io/yo/api:1.2.', 'Enter the Docker image, for example ghcr.io/me/api:1.2.')
  else if (!IMAGE_RE.test(d.image.trim())) e.image = m(lang, 'Nombre de imagen no válido: minúsculas, números y . _ / : @ - (sin espacios).', 'Not a valid image name: lowercase letters, digits and . _ / : @ - (no spaces).')

  const portErr = validatePorts(d.ports, ctx.usedPorts, lang)
  if (portErr) e.ports = portErr

  const keys: string[] = []
  const envRows = d.env.filter((r) => r.key.trim() || r.value)
  if (envRows.length > LIMITS.envEntries) e.env = m(lang, `Como máximo ${LIMITS.envEntries} variables.`, `At most ${LIMITS.envEntries} variables.`)
  for (const r of envRows) {
    const k = r.key.trim()
    const kErr = validateKey(k, lang)
    if (kErr) { e.env = kErr; break }
    if (r.value.length > LIMITS.envValue) { e.env = m(lang, `El valor de ${k} supera ${LIMITS.envValue} caracteres.`, `The value of ${k} is over ${LIMITS.envValue} characters.`); break }
    if (CONTROL_RE.test(r.value)) { e.env = m(lang, `El valor de ${k} tiene caracteres de control.`, `The value of ${k} has control characters.`); break }
    keys.push(k)
  }
  for (const k of d.secretKeys) {
    const kErr = validateKey(k, lang)
    if (kErr) { e.secretKeys = kErr; break }
    keys.push(k)
  }
  const dup = keys.find((k, i) => keys.indexOf(k) !== i)
  if (dup) {
    const msg = m(lang, `"${dup}" está repetida: cada nombre solo puede estar una vez entre variables y secretos.`, `"${dup}" is repeated: each name can appear only once across variables and secrets.`)
    if (d.secretKeys.includes(dup)) e.secretKeys = e.secretKeys ?? msg
    else e.env = e.env ?? msg
  }

  const cmd = parseCommand(d.command)
  if (cmd.error) e.command = m(lang, 'Falta cerrar unas comillas.', 'A quote is not closed.')
  else if (cmd.args.length > LIMITS.commandItems) e.command = m(lang, `Como máximo ${LIMITS.commandItems} argumentos.`, `At most ${LIMITS.commandItems} arguments.`)
  else if (cmd.args.some((a) => a.length > LIMITS.commandItem)) e.command = m(lang, `Cada argumento admite ${LIMITS.commandItem} caracteres como máximo.`, `Each argument can be at most ${LIMITS.commandItem} characters.`)

  if (d.volume.trim() && !VOLUME_RE.test(d.volume.trim())) e.volume = m(lang, 'Debe ser una ruta absoluta dentro del contenedor, como /data.', 'Must be an absolute path inside the container, like /data.')
  if (d.notes.length > LIMITS.notes) e.notes = m(lang, `Máximo ${LIMITS.notes} caracteres.`, `At most ${LIMITS.notes} characters.`)
  return e
}

/** The body for PUT /api/registry/images/{id}. */
export function imageBody(d: ImageDraft) {
  return {
    name: d.name.trim(),
    image: d.image.trim(),
    ports: d.ports.map((p) => Number(p.trim())),
    env: d.env.filter((r) => r.key.trim() || r.value).map((r) => ({ key: r.key.trim(), value: r.value })),
    secretKeys: d.secretKeys,
    command: parseCommand(d.command).args,
    volume: d.volume.trim() || null,
    notes: d.notes.trim(),
  }
}

// ---- sources ----------------------------------------------------------------------------------------------------------

export interface SourceDraft {
  id: string
  kind: 'local' | 'git'
  path: string
  url: string
  branch: string
  isNew: boolean
}

export function emptySourceDraft(): SourceDraft {
  return { id: '', kind: 'local', path: '', url: '', branch: '', isNew: true }
}

/** Absolute on Windows (C:\x, C:/x, \\server\share) or POSIX (/x). */
export function isAbsolutePath(p: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(p) || p.startsWith('\\\\') || p.startsWith('/')
}

/** https://host/path[.git], ssh://git@host/path or git@host:path. Nothing else, nothing starting with "-". */
export function isGitUrl(u: string): boolean {
  if (!u || u.startsWith('-') || /\s/.test(u) || CONTROL_RE.test(u)) return false
  return /^https:\/\/[^/\s]+\/\S+$/.test(u) || /^ssh:\/\/git@[^/\s]+\/\S+$/.test(u) || /^git@[^:\s/]+:\S+$/.test(u)
}

export function validateSource(d: SourceDraft, takenIds: string[], lang: Lang): Errors {
  const e: Errors = {}
  if (d.isNew) {
    const idErr = validateId(d.id, takenIds, lang)
    if (idErr) e.id = idErr
  }
  if (d.kind === 'local') {
    if (!d.path.trim()) e.path = m(lang, 'Indica la carpeta del repositorio, con ruta completa.', 'Enter the repository folder, with its full path.')
    else if (!isAbsolutePath(d.path.trim())) e.path = m(lang, 'La ruta debe ser completa, por ejemplo C:\\code\\mi-repo.', 'The path must be complete, for example C:\\code\\my-repo.')
  } else {
    if (!d.url.trim()) e.url = m(lang, 'Indica la dirección del repositorio.', 'Enter the repository address.')
    else if (!isGitUrl(d.url.trim())) e.url = m(lang, 'Usa https://servidor/ruta.git, ssh://git@servidor/ruta o git@servidor:ruta.', 'Use https://host/path.git, ssh://git@host/path or git@host:path.')
    if (d.branch.trim() && (!BRANCH_RE.test(d.branch.trim()) || d.branch.includes('..'))) e.branch = m(lang, 'Nombre de rama no válido.', 'Not a valid branch name.')
  }
  return e
}

export function sourceBody(d: SourceDraft) {
  return d.kind === 'local' ? { kind: 'local' as const, path: d.path.trim() } : { kind: 'git' as const, url: d.url.trim(), branch: d.branch.trim() || null }
}

export type { Tone }

export function sourceTone(s: SourceDef['status']): Tone {
  return s === 'ok' ? 'ok' : s === 'error' || s === 'missing' ? 'bad' : 'warn'
}

export function sourceStatusLabel(s: SourceDef['status'], lang: Lang): string {
  switch (s) {
    case 'ok': return m(lang, 'Al día', 'Ready')
    case 'missing': return m(lang, 'No encontrada', 'Missing')
    case 'not-cloned': return m(lang, 'Sin clonar', 'Not cloned')
    default: return m(lang, 'Con error', 'Error')
  }
}

/** One line about the state of a repository: branch, short commit, how far behind, local changes. */
export function sourceDetail(s: SourceDef, lang: Lang): string {
  const parts: string[] = []
  if (s.currentBranch) parts.push(s.currentBranch)
  if (s.head) parts.push(s.head.slice(0, 7))
  if (s.behind && s.behind > 0) parts.push(m(lang, s.behind === 1 ? '1 cambio nuevo por traer' : `${s.behind} cambios nuevos por traer`, s.behind === 1 ? '1 new change to pull' : `${s.behind} new changes to pull`))
  if (s.dirty) parts.push(m(lang, 'con cambios sin guardar', 'with uncommitted changes'))
  return parts.join(' · ')
}

/** What the sync button says: a git source never cloned is "Clone", otherwise "Sync"; a local folder only refreshes. */
export function syncLabel(s: SourceDef, lang: Lang): string {
  if (s.kind === 'local') return m(lang, 'Actualizar estado', 'Refresh status')
  return s.status === 'not-cloned' ? m(lang, 'Clonar', 'Clone') : m(lang, 'Sincronizar', 'Sync')
}

// ---- compose profiles -------------------------------------------------------------------------------------------------

export interface ComposeDraft {
  id: string
  name: string
  purpose: string
  files: Array<{ source: string; path: string }>
  profiles: string[]
  /** Empty path means "no env file". */
  envFile: { source: string; path: string }
  isNew: boolean
}

export function emptyComposeDraft(): ComposeDraft {
  return { id: '', name: '', purpose: '', files: [{ source: '', path: '' }], profiles: [], envFile: { source: '', path: '' }, isNew: true }
}

export function composeToDraft(c: ComposeDef): ComposeDraft {
  return {
    id: c.id,
    name: c.name,
    purpose: c.purpose,
    files: c.files.length > 0 ? c.files.map((f) => ({ source: f.source, path: f.path })) : [{ source: '', path: '' }],
    profiles: [...c.profiles],
    envFile: c.envFile ? { source: c.envFile.source, path: c.envFile.path } : { source: '', path: '' },
    isNew: false,
  }
}

const baseName = (p: string) => p.split(/[\\/]/).pop() ?? p

export function isEnvFileName(p: string): boolean {
  const n = baseName(p).toLowerCase()
  return n === '.env' || n.startsWith('.env.') || n.endsWith('.env')
}

/** Problem with one file path, or ''. A relative path must stay inside its source folder. */
export function validateFileRef(ref: ComposeFileRef, kind: 'compose' | 'env', lang: Lang): string {
  const p = ref.path.trim()
  if (!p) return m(lang, 'Falta la ruta del archivo.', 'The file path is missing.')
  if (CONTROL_RE.test(p)) return m(lang, 'La ruta tiene caracteres no válidos.', 'The path has invalid characters.')
  if (ref.source === '') {
    if (!isAbsolutePath(p)) return m(lang, 'Sin repositorio la ruta debe ser completa, o elige un repositorio.', 'Without a repository the path must be complete, or pick a repository.')
  } else {
    if (isAbsolutePath(p)) return m(lang, 'Con repositorio la ruta es relativa a su carpeta, sin empezar por / ni letra de unidad.', 'With a repository the path is relative to its folder, not starting with / or a drive letter.')
    if (p.split(/[\\/]/).includes('..')) return m(lang, 'La ruta no puede salir de la carpeta del repositorio (sin "..").', 'The path cannot leave the repository folder (no "..").')
  }
  if (kind === 'compose' && !/\.(ya?ml)$/i.test(p)) return m(lang, 'Un archivo de compose termina en .yml o .yaml.', 'A compose file ends in .yml or .yaml.')
  if (kind === 'env' && !isEnvFileName(p)) return m(lang, 'Un archivo de variables se llama .env, .env.algo o algo.env.', 'An env file is named .env, .env.something or something.env.')
  return ''
}

export interface ComposeErrors extends Errors {
  /** Per file row (index as text), plus "env". */
}

export function validateCompose(d: ComposeDraft, takenIds: string[], sourceIds: string[], lang: Lang): ComposeErrors {
  const e: ComposeErrors = {}
  if (d.isNew) {
    const idErr = validateId(d.id, takenIds, lang)
    if (idErr) e.id = idErr
  }
  if (!d.name.trim()) e.name = m(lang, 'Ponle un nombre.', 'Give it a name.')
  else if (d.name.trim().length > LIMITS.name) e.name = m(lang, `Máximo ${LIMITS.name} caracteres.`, `At most ${LIMITS.name} characters.`)
  if (d.purpose.length > LIMITS.purpose) e.purpose = m(lang, `Máximo ${LIMITS.purpose} caracteres.`, `At most ${LIMITS.purpose} characters.`)
  if (d.files.length === 0) e.files = m(lang, 'Añade al menos un archivo de compose.', 'Add at least one compose file.')
  d.files.forEach((f, i) => {
    if (f.source && !sourceIds.includes(f.source)) e[`file${i}`] = m(lang, 'Ese repositorio ya no existe.', 'That repository no longer exists.')
    else {
      const err = validateFileRef(f, 'compose', lang)
      if (err) e[`file${i}`] = err
    }
  })
  const bad = d.profiles.find((p) => !PROFILE_RE.test(p))
  if (bad) e.profiles = m(lang, `"${bad}" no es un nombre de perfil válido.`, `"${bad}" is not a valid profile name.`)
  if (d.envFile.path.trim()) {
    if (d.envFile.source && !sourceIds.includes(d.envFile.source)) e.env = m(lang, 'Ese repositorio ya no existe.', 'That repository no longer exists.')
    else {
      const err = validateFileRef(d.envFile, 'env', lang)
      if (err) e.env = err
    }
  }
  return e
}

export function composeBody(d: ComposeDraft) {
  return {
    name: d.name.trim(),
    purpose: d.purpose.trim(),
    files: d.files.map((f) => ({ source: f.source, path: f.path.trim() })),
    profiles: d.profiles,
    envFile: d.envFile.path.trim() ? { source: d.envFile.source, path: d.envFile.path.trim() } : null,
  }
}

export function fileTone(s: ComposeFileStatus['status']): Tone {
  return s === 'ok' ? 'ok' : s === 'changed' ? 'warn' : 'bad'
}

export function fileStatusLabel(s: ComposeFileStatus['status'], lang: Lang): string {
  switch (s) {
    case 'ok': return m(lang, 'Sin cambios', 'Unchanged')
    case 'changed': return m(lang, 'Cambió desde el último "Subir"', 'Changed since the last "Up"')
    default: return m(lang, 'No encontrado', 'Missing')
  }
}

export function missingFiles(c: ComposeDef): string[] {
  return [...c.fileStatus, ...(c.envFileStatus ? [c.envFileStatus] : [])].filter((f) => f.status === 'missing').map((f) => f.path)
}

export function changedFiles(c: ComposeDef): string[] {
  return [...c.fileStatus, ...(c.envFileStatus ? [c.envFileStatus] : [])].filter((f) => f.status === 'changed').map((f) => f.path)
}

export function composeTone(c: Pick<ComposeDef, 'state'>): Tone {
  const { running, total } = c.state
  if (total > 0 && running === total) return 'ok'
  return running > 0 ? 'warn' : 'idle'
}

export function composeSummary(c: Pick<ComposeDef, 'state'>, lang: Lang): string {
  const { running, total } = c.state
  if (total === 0) return m(lang, 'Parado', 'Stopped')
  if (running === 0) return m(lang, `Parado · ${total} servicios`, `Stopped · ${total} services`)
  return m(lang, `${running} de ${total} en marcha`, `${running} of ${total} running`)
}

/** Why "Up" cannot be pressed right now, or '' when it can. */
export function upBlocker(c: ComposeDef, dockerUp: boolean, lang: Lang): string {
  if (!dockerUp) return m(lang, 'Docker no está disponible.', 'Docker is not available.')
  const missing = missingFiles(c)
  if (missing.length > 0) return m(lang, `Faltan archivos: ${missing.join(', ')}`, `Missing files: ${missing.join(', ')}`)
  return ''
}

/** Short name of a path for a pill or a list: the last two segments. */
export function shortPath(p: string): string {
  const parts = p.split(/[\\/]/).filter(Boolean)
  return parts.length <= 2 ? p : `…/${parts.slice(-2).join('/')}`
}

/** Label of one compose file row in the form and the card: "my-repo: deploy/compose.yml". */
export function fileRefLabel(ref: ComposeFileRef): string {
  return ref.source ? `${ref.source}: ${ref.path}` : ref.path
}
