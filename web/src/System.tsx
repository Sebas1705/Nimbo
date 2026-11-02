import { useCallback, useEffect, useRef, useState } from 'react'
import {
  images as imagesApi,
  system,
  updates as updatesApi,
  formatBytes,
  type DataInfo,
  type ImagesView,
  type Provider,
  type ResetWhat,
  type SecretsView,
  type Settings,
  type StatsView,
  type UpdateStatus,
} from './api'
import type { Key, Lang } from './i18n'
import { ago, backupName, describeEntry, formatUptime, installerFor, maskSecret, parseBackup, usageRanking } from './systemLogic'

type T = (k: Key) => string

const pick = (lang: Lang, es: string, en: string) => (lang === 'es' ? es : en)
const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Runs a loader now and then again every few seconds; the screens below are all "load, show, act, reload". */
function useLoad<T>(load: () => Promise<T>, everyMs = 0): { data: T | null; error: string; reload: () => void } {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const loadRef = useRef(load)
  loadRef.current = load
  const reload = useCallback(() => {
    loadRef.current().then((d) => { setData(d); setError('') }).catch((e) => setError(message(e)))
  }, [])
  useEffect(() => {
    reload()
    if (!everyMs) return
    const id = setInterval(reload, everyMs)
    return () => clearInterval(id)
  }, [reload, everyMs])
  return { data, error, reload }
}

/** One action button with its own "working" and error state, so a slow download does not freeze the rest of the screen. */
function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const run = async (fn: () => Promise<string | void>) => {
    setBusy(true); setError(''); setDone('')
    try { setDone((await fn()) || '') } catch (e) { setError(message(e)) } finally { setBusy(false) }
  }
  return { busy, error, done, run }
}

function Status({ error, done }: { error: string; done: string }) {
  return (
    <>
      {error && <p className="err" role="alert">{error}</p>}
      {done && <p className="ok-note" role="status">{done}</p>}
    </>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Activity
// ---------------------------------------------------------------------------------------------------------------------

export function ActivityView({ lang }: { t: T; lang: Lang }) {
  const { data, error } = useLoad<StatsView>(system.stats, 10_000)
  if (!data) return <p className="muted">{error || '…'}</p>
  const now = Date.now()
  const ranking = usageRanking(data.stats.providers)
  const c = data.stats.counters
  const lastRun = data.fidelity[data.fidelity.length - 1]
  const prevRun = data.fidelity[data.fidelity.length - 2]

  return (
    <div className="sys">
      <div className="sys-grid">
        <Card title={pick(lang, 'Desde', 'Since')}>
          <b className="big">{ago(data.stats.since, now, lang)}</b>
          <small>{pick(lang, 'Las estadísticas empiezan aquí y se quedan en tu equipo.', 'Statistics start here and stay on your computer.')}</small>
        </Card>
        <Card title={pick(lang, 'Arranques', 'Starts')}><b className="big">{c['provider.start'] ?? 0}</b></Card>
        <Card title={pick(lang, 'Laboratorios verificados', 'Labs verified')}><b className="big">{c['lab.verify'] ?? 0}</b><small>{pick(lang, `${c['lab.complete'] ?? 0} completados`, `${c['lab.complete'] ?? 0} completed`)}</small></Card>
        <Card title={pick(lang, 'Fidelidad', 'Fidelity')}>
          <b className="big">{lastRun ? `${Math.round((lastRun.passed / Math.max(1, lastRun.steps)) * 100)} %` : '—'}</b>
          <small>{lastRun ? ago(lastRun.at, now, lang) : pick(lang, 'Aún no has ejecutado la matriz.', 'You have not run the matrix yet.')}</small>
        </Card>
      </div>

      <section className="sys-card">
        <h3>{pick(lang, 'Tiempo en marcha por emulador', 'Time up per emulator')}</h3>
        {ranking.length === 0 ? (
          <p className="muted">{pick(lang, 'Arranca algún emulador y aquí verás cuánto lo usas.', 'Start an emulator and you will see how much you use it here.')}</p>
        ) : (
          <table className="sys-table">
            <thead><tr><th>{pick(lang, 'Emulador', 'Emulator')}</th><th>{pick(lang, 'Tiempo', 'Time')}</th><th>{pick(lang, 'Arranques', 'Starts')}</th><th>{pick(lang, 'Último', 'Last')}</th></tr></thead>
            <tbody>
              {ranking.map(([id, u]) => (
                <tr key={id}>
                  <td><code>{id}</code></td>
                  <td>{formatUptime(u.upSeconds)}</td>
                  <td>{u.starts}</td>
                  <td>{u.lastStartedAt ? ago(u.lastStartedAt, now, lang) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {lastRun && (
        <section className="sys-card">
          <h3>{pick(lang, 'Fidelidad a lo largo del tiempo', 'Fidelity over time')}</h3>
          <div className="spark" role="img" aria-label={pick(lang, 'Porcentaje de pasos superados en cada ejecución', 'Share of steps passed in each run')}>
            {data.fidelity.map((r) => {
              const pct = Math.round((r.passed / Math.max(1, r.steps)) * 100)
              return <i key={r.at} style={{ height: `${Math.max(6, pct)}%` }} title={`${pct} % · ${ago(r.at, now, lang)}`} />
            })}
          </div>
          {prevRun && <Regressions prev={prevRun.suites} cur={lastRun.suites} lang={lang} />}
        </section>
      )}

      <section className="sys-card">
        <h3>{pick(lang, 'Diario', 'Journal')}</h3>
        {data.journal.length === 0 ? (
          <p className="muted">{pick(lang, 'Nada todavía.', 'Nothing yet.')}</p>
        ) : (
          <ul className="journal">
            {data.journal.map((e, i) => (
              <li key={`${e.at}-${i}`}>
                <span>{describeEntry(e, lang)}</span>
                <time dateTime={new Date(e.at).toISOString()}>{ago(e.at, now, lang)}</time>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function Regressions({ prev, cur, lang }: { prev: Record<string, string>; cur: Record<string, string>; lang: Lang }) {
  const rank: Record<string, number> = { full: 3, partial: 2, missing: 1, offline: 0 }
  const changes = Object.entries(cur)
    .filter(([id, status]) => prev[id] && prev[id] !== status)
    .map(([id, status]) => ({ id, from: prev[id], to: status, worse: prev[id] !== 'offline' && status !== 'offline' && rank[status] < rank[prev[id]] }))
  if (changes.length === 0) return <p className="muted">{pick(lang, 'Sin cambios respecto a la ejecución anterior.', 'No changes since the previous run.')}</p>
  return (
    <ul className="journal">
      {changes.map((c) => (
        <li key={c.id} className={c.worse ? 'worse' : undefined}>
          <span><code>{c.id}</code>: {c.from} → {c.to}</span>
          <b>{c.worse ? pick(lang, 'Empeoró', 'Got worse') : pick(lang, 'Cambió', 'Changed')}</b>
        </li>
      ))}
    </ul>
  )
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="sys-card stat">
      <h3>{title}</h3>
      {children}
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Settings and data
// ---------------------------------------------------------------------------------------------------------------------

interface SettingsProps {
  t: T
  lang: Lang
  providers: Provider[]
  settings: Settings | null
  onSettings: (patch: Partial<Pick<Settings, 'lang' | 'theme' | 'updateMode'>>) => Promise<void>
  onChanged: () => void
}

export function SettingsView({ lang, providers, settings, onSettings, onChanged }: SettingsProps) {
  const info = useLoad<DataInfo>(system.data)
  const secrets = useLoad<SecretsView>(system.secrets)
  const persistable = providers.filter((p) => p.persistable)

  return (
    <div className="sys">
      <section className="sys-card">
        <h3>{pick(lang, 'Preferencias', 'Preferences')}</h3>
        <div className="kv">
          <label>{pick(lang, 'Idioma', 'Language')}
            <select value={settings?.lang ?? lang} onChange={(e) => onSettings({ lang: e.target.value as 'es' | 'en' })}>
              <option value="es">Español</option><option value="en">English</option>
            </select>
          </label>
          <label>{pick(lang, 'Tema', 'Theme')}
            <select value={settings?.theme ?? 'auto'} onChange={(e) => onSettings({ theme: e.target.value as Settings['theme'] })}>
              <option value="auto">{pick(lang, 'Automático', 'Automatic')}</option>
              <option value="light">{pick(lang, 'Claro', 'Light')}</option>
              <option value="dark">{pick(lang, 'Oscuro', 'Dark')}</option>
            </select>
          </label>
          <label>{pick(lang, 'Buscar actualizaciones', 'Look for updates')}
            <select value={settings?.updateMode ?? 'auto'} onChange={(e) => onSettings({ updateMode: e.target.value as Settings['updateMode'] })}>
              <option value="auto">{pick(lang, 'Automático (una vez al día)', 'Automatic (once a day)')}</option>
              <option value="manual">{pick(lang, 'Solo cuando yo lo pida', 'Only when I ask')}</option>
            </select>
          </label>
        </div>
      </section>

      <section className="sys-card">
        <h3>{pick(lang, 'Datos de los emuladores', 'Emulator data')}</h3>
        <p className="muted">{pick(lang, 'Los datos de un emulador sobreviven a pararlo o eliminarlo si activas "Conservar datos" en su tarjeta de Infraestructura.', 'An emulator\'s data survives stopping or removing it if you switch on "Keep data" on its card in Infrastructure.')}</p>
        <table className="sys-table">
          <thead><tr><th>{pick(lang, 'Emulador', 'Emulator')}</th><th>{pick(lang, 'Conservar', 'Keep')}</th><th>{pick(lang, 'Datos guardados', 'Saved data')}</th></tr></thead>
          <tbody>
            {persistable.map((p) => (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td>{p.persist ? pick(lang, 'Sí', 'Yes') : 'No'}</td>
                <td>{p.hasData ? pick(lang, 'Sí', 'Yes') : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="muted">{pick(lang, 'Con PostgreSQL conservado, la contraseña queda fijada al crear la base de datos.', 'With PostgreSQL data kept, the password is fixed when the database is created.')}</p>
      </section>

      <SecretsCard lang={lang} view={secrets.data} error={secrets.error} reload={() => { secrets.reload(); onChanged() }} />

      <section className="sys-card">
        <h3>{pick(lang, 'Carpeta de datos', 'Data folder')}</h3>
        {info.data ? (
          <>
            <p><code>{info.data.location ?? pick(lang, '(en memoria)', '(in memory)')}</code></p>
            <ul className="journal">
              {Object.entries(info.data.files).map(([name, size]) => <li key={name}><span><code>{name}</code></span><b>{formatBytes(size)}</b></li>)}
            </ul>
          </>
        ) : <p className="muted">{info.error || '…'}</p>}
        <BackupControls lang={lang} onChanged={() => { info.reload(); onChanged() }} />
      </section>
    </div>
  )
}

function SecretsCard({ lang, view, error, reload }: { lang: Lang; view: SecretsView | null; error: string; reload: () => void }) {
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [shown, setShown] = useState<{ name: string; value: string } | null>(null)
  const act = useAction()
  if (!view) return <section className="sys-card"><h3>{pick(lang, 'Contraseñas', 'Passwords')}</h3><p className="muted">{error || '…'}</p></section>

  return (
    <section className="sys-card">
      <h3>{pick(lang, 'Contraseñas y usuarios', 'Passwords and users')}</h3>
      <p className="muted">
        {view.available
          ? pick(lang, 'Se guardan cifradas con tu usuario de Windows. Una variable de entorno con el mismo nombre tiene prioridad.', 'They are stored encrypted for your Windows user. An environment variable with the same name takes priority.')
          : view.reason}
      </p>
      <ul className="secrets">
        {view.items.map((s) => (
          <li key={s.name}>
            <div>
              <code>{s.name}</code>
              <small>{pick(lang, 'Lo usan: ', 'Used by: ')}{s.usedBy.join(', ')}</small>
            </div>
            <span className="mask" aria-label={s.set ? pick(lang, 'guardado', 'stored') : pick(lang, 'sin guardar', 'not stored')}>{maskSecret(s.set) || '—'}</span>
            {view.available && (
              <div className="actions">
                <input
                  type={s.password ? 'password' : 'text'}
                  autoComplete="off"
                  placeholder={s.password ? pick(lang, 'Nueva contraseña', 'New password') : pick(lang, 'Nuevo usuario', 'New user')}
                  value={draft[s.name] ?? ''}
                  onChange={(e) => setDraft((d) => ({ ...d, [s.name]: e.target.value }))}
                />
                <button disabled={act.busy || !(draft[s.name] ?? '')} onClick={() => act.run(async () => { await system.setSecret(s.name, draft[s.name]); setDraft((d) => ({ ...d, [s.name]: '' })); reload(); return pick(lang, 'Guardado.', 'Saved.') })}>
                  {pick(lang, 'Guardar', 'Save')}
                </button>
                {s.password && (
                  <button className="ghost" disabled={act.busy} onClick={() => act.run(async () => { const r = await system.generateSecret(s.name); setShown({ name: s.name, value: r.value }); reload() })}>
                    {pick(lang, 'Generar', 'Generate')}
                  </button>
                )}
                {s.set && <button className="ghost danger" disabled={act.busy} onClick={() => act.run(async () => { await system.deleteSecret(s.name); reload() })}>{pick(lang, 'Borrar', 'Delete')}</button>}
              </div>
            )}
          </li>
        ))}
      </ul>
      {shown && (
        <div className="banner" role="status">
          <b>{pick(lang, 'Contraseña generada para ', 'Generated password for ')}{shown.name}</b>
          <p>{pick(lang, 'Cópiala ahora: no se vuelve a mostrar.', 'Copy it now: it is not shown again.')}</p>
          <code className="secret-once">{shown.value}</code>
          <div className="actions">
            <button onClick={() => { try { void navigator.clipboard.writeText(shown.value) } catch { /* the value is on screen anyway */ } }}>{pick(lang, 'Copiar', 'Copy')}</button>
            <button className="ghost" onClick={() => setShown(null)}>{pick(lang, 'Ya la tengo', 'I have it')}</button>
          </div>
        </div>
      )}
      <Status error={act.error} done={act.done} />
    </section>
  )
}

function BackupControls({ lang, onChanged }: { lang: Lang; onChanged: () => void }) {
  const act = useAction()
  const [what, setWhat] = useState<ResetWhat>('stats')
  const file = useRef<HTMLInputElement>(null)

  const download = () => act.run(async () => {
    const bundle = await system.exportData()
    const url = URL.createObjectURL(new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url; a.download = backupName(new Date()); a.click()
    URL.revokeObjectURL(url)
    return pick(lang, 'Copia descargada. No incluye contraseñas ni datos de emuladores.', 'Backup downloaded. It holds no passwords or emulator data.')
  })

  const restore = (f: File | undefined) => {
    if (!f) return
    act.run(async () => {
      const parsed = parseBackup(await f.text())
      if (!parsed.ok) throw new Error(pick(lang, 'Ese archivo no es una copia de Nimbo.', 'That file is not a Nimbo backup.'))
      if (!window.confirm(pick(lang, 'Restaurar sustituye tus ajustes, progreso y estadísticas actuales. ¿Continuar?', 'Restoring replaces your current settings, progress and statistics. Continue?'))) return
      const r = await system.importData(parsed.bundle)
      onChanged()
      return pick(lang, `Restaurado: ${r.restored.join(', ')}. Recarga la página para ver todos los cambios.`, `Restored: ${r.restored.join(', ')}. Reload the page to see every change.`)
    }).finally(() => { if (file.current) file.current.value = '' })
  }

  const reset = () => {
    if (!window.confirm(pick(lang, 'Esto borra datos de Nimbo y no se puede deshacer. ¿Continuar?', 'This deletes Nimbo data and cannot be undone. Continue?'))) return
    act.run(async () => { await system.reset(what); onChanged(); return pick(lang, 'Hecho.', 'Done.') })
  }

  return (
    <div className="backup">
      <div className="actions">
        <button disabled={act.busy} onClick={download}>{pick(lang, 'Descargar copia', 'Download backup')}</button>
        <button className="ghost" disabled={act.busy} onClick={() => file.current?.click()}>{pick(lang, 'Restaurar copia…', 'Restore backup…')}</button>
        <input ref={file} type="file" accept="application/json,.json" hidden onChange={(e) => restore(e.target.files?.[0])} />
      </div>
      <div className="actions">
        <select value={what} onChange={(e) => setWhat(e.target.value as ResetWhat)} aria-label={pick(lang, 'Qué borrar', 'What to delete')}>
          <option value="stats">{pick(lang, 'Estadísticas y diario', 'Statistics and journal')}</option>
          <option value="history">{pick(lang, 'Historial de fidelidad', 'Fidelity history')}</option>
          <option value="progress">{pick(lang, 'Progreso de laboratorios', 'Lab progress')}</option>
          <option value="all">{pick(lang, 'Todo lo anterior', 'All of the above')}</option>
        </select>
        <button className="ghost danger" disabled={act.busy} onClick={reset}>{pick(lang, 'Borrar', 'Delete')}</button>
      </div>
      <Status error={act.error} done={act.done} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Updates
// ---------------------------------------------------------------------------------------------------------------------

export function UpdatesView({ lang, status, onStatus }: { t: T; lang: Lang; status: UpdateStatus | null; onStatus: (s: UpdateStatus) => void }) {
  const act = useAction()
  const [path, setPath] = useState('')
  const [sha, setSha] = useState('')
  if (!status) return <p className="muted">…</p>

  const refresh = async () => onStatus(await updatesApi.status())
  const v = status.latest?.version
  const installer = installerFor(status.files, v)
  const now = Date.now()

  return (
    <div className="sys">
      <section className="sys-card">
        <h3>{pick(lang, 'Versión instalada', 'Installed version')}: <code>{status.current ? `v${status.current}` : 'dev'}</code></h3>
        <p className="muted">
          {status.checkedAt ? pick(lang, `Última búsqueda ${ago(status.checkedAt, now, lang)}.`, `Last checked ${ago(status.checkedAt, now, lang)}.`) : pick(lang, 'Aún no se ha buscado.', 'Not checked yet.')}{' '}
          {status.mode === 'manual' ? pick(lang, 'Modo manual: Nimbo no contacta con nadie por su cuenta.', 'Manual mode: Nimbo contacts nobody by itself.') : pick(lang, 'Modo automático: como mucho una vez al día.', 'Automatic mode: at most once a day.')}
        </p>
        <div className="actions">
          <button className="primary" disabled={act.busy} onClick={() => act.run(async () => { onStatus(await updatesApi.check(true)) })}>{act.busy ? '…' : pick(lang, 'Buscar ahora', 'Check now')}</button>
        </div>
        {status.error && <p className="err">{status.error}</p>}
        {!status.error && status.latest && !status.available && <p className="ok-note">{pick(lang, 'Tienes la última versión.', 'You are up to date.')}</p>}
      </section>

      {status.available && status.latest && (
        <section className="sys-card highlight">
          <h3>{pick(lang, `Nimbo ${status.latest.version} disponible`, `Nimbo ${status.latest.version} available`)}</h3>
          <pre className="notes">{status.latest.notes.trim() || pick(lang, 'Sin notas.', 'No notes.')}</pre>

          {status.asset ? (
            <p className="muted"><code>{status.asset.name}</code> · {formatBytes(status.asset.size)}</p>
          ) : status.manual ? (
            <p>{pick(lang, 'Esta instalación no se actualiza sola. ', 'This installation does not update itself. ')}<code>{status.manual}</code></p>
          ) : null}

          <div className="actions">
            {status.asset && !installer && (
              <button className="primary" disabled={act.busy} onClick={() => act.run(async () => { await updatesApi.download(status.latest!.version); await refresh(); return pick(lang, 'Descargada y verificada con SHA256SUMS.', 'Downloaded and verified against SHA256SUMS.') })}>
                {act.busy ? pick(lang, 'Descargando…', 'Downloading…') : pick(lang, 'Descargar y verificar', 'Download and verify')}
              </button>
            )}
            {installer && status.canInstall && (
              <button className="primary" disabled={act.busy} onClick={() => {
                if (!window.confirm(pick(lang, `Nimbo se cerrará, instalará la ${installer.version} y se abrirá solo. Tus emuladores seguirán en Docker. ¿Instalar?`, `Nimbo will close, install ${installer.version} and reopen by itself. Your emulators stay in Docker. Install?`))) return
                act.run(async () => (await updatesApi.install(installer.version)).message)
              }}>
                {pick(lang, 'Instalar ahora', 'Install now')}
              </button>
            )}
          </div>
          {installer && (
            <p className={installer.verified ? 'ok-note' : 'err'}>
              {installer.verified ? pick(lang, 'Verificado con el SHA256SUMS de la versión.', 'Verified against the release SHA256SUMS.') : pick(lang, 'Sin verificar: no había una suma de comprobación publicada.', 'Not verified: no published checksum was available.')}
              {' '}<code title={installer.sha256}>{installer.sha256.slice(0, 16)}…</code>
              {installer && !status.canInstall && <> {pick(lang, 'Archivo en la carpeta de datos, listo para usar.', 'File in the data folder, ready to use.')}</>}
            </p>
          )}
        </section>
      )}

      <section className="sys-card">
        <h3>{pick(lang, 'Instalar desde un archivo (sin internet)', 'Install from a file (no internet)')}</h3>
        <p className="muted">{pick(lang, 'Indica la ruta de un instalador de Nimbo que ya tengas (por ejemplo, del kit sin conexión). Si hay un SHA256SUMS a su lado o escribes la suma, se comprueba.', 'Give the path of a Nimbo installer you already have (for example from the offline kit). If a SHA256SUMS sits next to it, or you type the checksum, it is verified.')}</p>
        <div className="actions wide">
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="D:\\nimbo-kit\\Nimbo-0.3.0-windows-x64.msi" aria-label={pick(lang, 'Ruta del instalador', 'Installer path')} />
          <input value={sha} onChange={(e) => setSha(e.target.value)} placeholder={pick(lang, 'SHA-256 esperado (opcional)', 'Expected SHA-256 (optional)')} aria-label="SHA-256" />
          <button disabled={act.busy || !path.trim()} onClick={() => act.run(async () => { const f = await updatesApi.importFile(path.trim(), sha.trim()); await refresh(); return pick(lang, `Importada la ${f.version}${f.verified ? ' (verificada)' : ' (sin verificar)'}.`, `Imported ${f.version}${f.verified ? ' (verified)' : ' (not verified)'}.`) })}>
            {pick(lang, 'Importar', 'Import')}
          </button>
        </div>
      </section>

      {status.files.length > 0 && (
        <section className="sys-card">
          <h3>{pick(lang, 'Descargadas', 'Downloaded')}</h3>
          <ul className="journal">
            {status.files.map((f) => (
              <li key={f.name}><span><code>{f.name}</code> · {formatBytes(f.size)}</span><b>{f.verified ? pick(lang, 'verificada', 'verified') : pick(lang, 'sin verificar', 'not verified')}</b></li>
            ))}
          </ul>
          <div className="actions"><button className="ghost danger" disabled={act.busy} onClick={() => act.run(async () => { await updatesApi.clean(); await refresh() })}>{pick(lang, 'Borrar descargas', 'Delete downloads')}</button></div>
        </section>
      )}
      <Status error={act.error} done={act.done} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Offline images
// ---------------------------------------------------------------------------------------------------------------------

export function OfflineView({ lang }: { t: T; lang: Lang }) {
  const { data, error, reload } = useLoad<ImagesView>(imagesApi.status, 15_000)
  const act = useAction()
  const [path, setPath] = useState('')
  if (!data) return <p className="muted">{error || '…'}</p>
  const present = data.images.filter((i) => i.present).length

  return (
    <div className="sys">
      <section className="sys-card">
        <h3>{pick(lang, 'Imágenes en este equipo', 'Images on this computer')}: <b>{present} / {data.images.length}</b></h3>
        {!data.docker && <p className="err">{pick(lang, 'Docker no responde, así que no se puede saber qué imágenes hay.', 'Docker is not responding, so the images cannot be listed.')}</p>}
        <table className="sys-table">
          <thead><tr><th>{pick(lang, 'Emulador', 'Emulator')}</th><th>{pick(lang, 'Imagen', 'Image')}</th><th>{pick(lang, 'Paquete', 'Pack')}</th><th /></tr></thead>
          <tbody>
            {data.images.map((i) => (
              <tr key={i.provider}>
                <td>{i.provider}</td>
                <td><code>{i.image}</code></td>
                <td>{i.pack ?? '—'}</td>
                <td>{i.present ? <b className="okc">✓</b> : (
                  <button className="ghost small" disabled={act.busy || !data.docker} onClick={() => act.run(async () => { await imagesApi.pull(i.provider); reload(); return pick(lang, `Descargada la imagen de ${i.provider}.`, `Pulled the ${i.provider} image.`) })}>{pick(lang, 'Descargar', 'Pull')}</button>
                )}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="sys-card">
        <h3>{pick(lang, 'Paquetes de imágenes', 'Image packs')}</h3>
        <p className="muted">{pick(lang, 'Busco en: ', 'Looking in: ')}{data.folders.map((f) => <code key={f} className="folder">{f}</code>)}</p>
        {data.files.length === 0 ? (
          <p className="muted">{pick(lang, 'No hay paquetes en esas carpetas. Copia ahí los archivos del kit o escribe una ruta abajo.', 'There are no packs in those folders. Copy the kit files there or type a path below.')}</p>
        ) : (
          <ul className="journal">
            {data.files.map((f) => (
              <li key={f.path}>
                <span><code>{f.name}</code> · {formatBytes(f.size)} · {f.verified === true ? pick(lang, 'verificado', 'verified') : f.verified === false ? pick(lang, 'NO COINCIDE', 'DOES NOT MATCH') : pick(lang, 'sin suma', 'no checksum')}</span>
                <button className="small" disabled={act.busy || !data.docker || f.verified === false} onClick={() => act.run(async () => { const r = await imagesApi.load(f.path); reload(); return pick(lang, `Cargadas ${r.loaded.length} imágenes.`, `Loaded ${r.loaded.length} images.`) })}>{pick(lang, 'Cargar', 'Load')}</button>
              </li>
            ))}
          </ul>
        )}
        <div className="actions wide">
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="D:\\nimbo-kit\\images\\Nimbo-0.3.0-images-data.tar.gz" aria-label={pick(lang, 'Ruta del paquete', 'Pack path')} />
          <button disabled={act.busy || !path.trim() || !data.docker} onClick={() => act.run(async () => { const r = await imagesApi.load(path.trim()); reload(); return pick(lang, `Cargadas ${r.loaded.length} imágenes.`, `Loaded ${r.loaded.length} images.`) })}>{pick(lang, 'Cargar archivo', 'Load file')}</button>
        </div>
        {act.busy && <p className="muted">{pick(lang, 'Trabajando… un paquete grande puede tardar varios minutos.', 'Working… a big pack can take several minutes.')}</p>}
        <Status error={act.error} done={act.done} />
      </section>

      <section className="sys-card">
        <h3>{pick(lang, 'Preparar un kit sin conexión', 'Prepare an offline kit')}</h3>
        <p className="muted">{pick(lang, 'En un equipo con internet, ejecuta esto una vez; después copia la carpeta al equipo sin conexión, instala el .msi y carga los paquetes desde aquí.', 'On a computer with internet, run this once; then copy the folder to the offline computer, install the .msi and load the packs from here.')}</p>
        <pre className="notes">powershell -File packaging\make-offline-kit.ps1 -Version &lt;x.y.z&gt; -Out D:\nimbo-kit</pre>
      </section>
    </div>
  )
}
