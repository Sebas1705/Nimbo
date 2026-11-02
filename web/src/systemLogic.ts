import type { JournalEntry, ProviderUsage, UpdateFile, UpdateStatus } from './api'
import type { Lang } from './i18n'

/** 45 s, 12 min, 3 h 05 min, 2 d 4 h. Time spent up is measured in 15 s steps, so seconds are enough at the low end. */
export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)} s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ${String(minutes % 60).padStart(2, '0')} min`
  return `${Math.floor(hours / 24)} d ${hours % 24} h`
}

/** "just now", "5 min ago", "yesterday"... in the person's language; falls back to the date for anything older than a week. */
export function ago(at: number, now: number, lang: Lang): string {
  const s = Math.max(0, Math.round((now - at) / 1000))
  const es = lang === 'es'
  if (s < 45) return es ? 'ahora mismo' : 'just now'
  const m = Math.round(s / 60)
  if (m < 60) return es ? `hace ${m} min` : `${m} min ago`
  const h = Math.round(m / 60)
  if (h < 24) return es ? `hace ${h} h` : `${h} h ago`
  const d = Math.round(h / 24)
  if (d === 1) return es ? 'ayer' : 'yesterday'
  if (d < 7) return es ? `hace ${d} días` : `${d} days ago`
  return new Date(at).toLocaleDateString(es ? 'es-ES' : 'en-GB')
}

/** Providers ordered by time spent up, the ones never used left out. */
export function usageRanking(providers: Record<string, ProviderUsage>): Array<[string, ProviderUsage]> {
  return Object.entries(providers)
    .filter(([, u]) => u.starts > 0 || u.upSeconds > 0)
    .sort((a, b) => b[1].upSeconds - a[1].upSeconds || b[1].starts - a[1].starts || a[0].localeCompare(b[0]))
}

/** Plain-words description of one journal line. Unknown kinds are shown as they are, so new ones never vanish. */
export function describeEntry(e: JournalEntry, lang: Lang): string {
  const es = lang === 'es'
  const d = e.detail ? ` (${e.detail})` : ''
  switch (e.kind) {
    case 'provider.start': return es ? `Arrancó ${e.subject}` : `Started ${e.subject}`
    case 'provider.stop': return es ? `Paró ${e.subject}` : `Stopped ${e.subject}`
    case 'provider.remove': return es ? `Eliminó el contenedor de ${e.subject}` : `Removed the ${e.subject} container`
    case 'provider.persist': return es ? `${e.detail === 'on' ? 'Activó' : 'Desactivó'} conservar datos de ${e.subject}` : `Turned ${e.detail === 'on' ? 'on' : 'off'} keeping data for ${e.subject}`
    case 'provider.data.delete': return es ? `Borró los datos guardados de ${e.subject}` : `Deleted the saved data of ${e.subject}`
    case 'stack.up': return es ? `Levantó el stack ${e.subject}` : `Brought up the ${e.subject} stack`
    case 'stack.down': return es ? `Bajó el stack ${e.subject}` : `Took down the ${e.subject} stack`
    case 'lab.verify': return es ? `Verificó el laboratorio ${e.subject}${d}` : `Verified lab ${e.subject}${d}`
    case 'lab.complete': return es ? `Completó el laboratorio ${e.subject}` : `Completed lab ${e.subject}`
    case 'fidelity.run': return es ? `Ejecutó la matriz de fidelidad (${e.subject})` : `Ran the fidelity matrix (${e.subject})`
    case 'export': return es ? `Exportó ${e.subject}${d}` : `Exported ${e.subject}${d}`
    case 'secret.set': return es ? `Guardó el secreto ${e.subject}` : `Saved the secret ${e.subject}`
    case 'secret.delete': return es ? `Borró el secreto ${e.subject}` : `Deleted the secret ${e.subject}`
    case 'update.check': return es ? 'Buscó actualizaciones' : 'Checked for updates'
    case 'update.download': return es ? `Descargó la versión ${e.subject}` : `Downloaded version ${e.subject}`
    case 'update.import': return es ? `Importó la versión ${e.subject}${d}` : `Imported version ${e.subject}${d}`
    case 'update.install': return es ? `Instaló la versión ${e.subject}` : `Installed version ${e.subject}`
    case 'images.load': return es ? `Cargó el paquete de imágenes ${e.subject}` : `Loaded the image pack ${e.subject}`
    case 'images.pull': return es ? `Descargó la imagen de ${e.subject}` : `Pulled the image of ${e.subject}`
    case 'secret.reveal': return es ? `Mostró el valor de ${e.subject}` : `Revealed the value of ${e.subject}`
    case 'variables.init': return es ? `Inicializó las variables de ${e.subject}` : `Initialized the variables of ${e.subject}`
    case 'variables.apply': return es ? `Aplicó las variables a ${e.subject}` : `Applied the variables to ${e.subject}`
    case 'settings.change': return es ? 'Cambió los ajustes' : 'Changed the settings'
    case 'data.import': return es ? 'Restauró una copia de seguridad' : 'Restored a backup'
    default: return `${e.kind} ${e.subject}`
  }
}

/** Whether to show the "new version" dot on the rail: only when there is something newer. */
export function updateDot(status: UpdateStatus | null): boolean {
  return status?.available === true
}

/** Which downloaded installer to offer for the version in the news: a verified one beats an unverified one. */
export function installerFor(files: UpdateFile[], version: string | undefined): UpdateFile | null {
  if (!version) return null
  const mine = files.filter((f) => f.version === version && f.name.endsWith('.msi'))
  return mine.find((f) => f.verified) ?? mine[0] ?? null
}

/** A MinIO/PostgreSQL password the person sees once after generating it must not be shown by accident later. */
export function maskSecret(isSet: boolean): string {
  return isSet ? '••••••••••••' : ''
}

/** Reads a backup file chosen in the browser and checks it has the shape the server expects before sending it. */
export function parseBackup(text: string): { ok: true; bundle: unknown } | { ok: false } {
  try {
    const v = JSON.parse(text)
    if (v && typeof v === 'object' && typeof v.format === 'number' && v.files && typeof v.files === 'object') return { ok: true, bundle: v }
  } catch {
    /* fall through */
  }
  return { ok: false }
}

/** Filename for a backup, stamped with the day so several can sit side by side. */
export function backupName(now: Date): string {
  return `nimbo-backup-${now.toISOString().slice(0, 10)}.json`
}

/** Lab ids to hand over to the server once: only well-formed ones, and only those the server does not know yet. */
export function idsToImport(local: Iterable<string>, server: Record<string, unknown>): string[] {
  return [...local].filter((id) => /^[a-z0-9-]{1,41}$/.test(id) && !(id in server))
}
