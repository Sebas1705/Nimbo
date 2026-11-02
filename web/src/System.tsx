import { useRef, useState } from 'react'
import {
  images as imagesApi,
  system,
  variables,
  updates as updatesApi,
  formatBytes,
  type DataInfo,
  type ImagesView,
  type Provider,
  type ResetWhat,
  type VariablesView,
  type Settings,
  type StatsView,
  type UpdateStatus,
} from './api'
import { NO_VARS_NOTE, VAR_HELP } from './guides'
import type { Key, Lang } from './i18n'
import { ago, backupName, describeEntry, formatUptime, installerFor, maskSecret, parseBackup, usageRanking } from './systemLogic'
import { Card, ConfirmButton, CopyButton, EmptyState, Facts, Feedback, Notice, Pill, Stat, pick, useAction, useLoad } from './ui'
import './system.css'

type T = (k: Key) => string

/** What a loader shows until it has data: the error if there is one, else a pause mark. */
function Loading({ error }: { error: string }) {
  return error ? <Notice tone="bad">{error}</Notice> : <p className="muted">…</p>
}

// ---------------------------------------------------------------------------------------------------------------------
// Activity
// ---------------------------------------------------------------------------------------------------------------------

export function ActivityView({ lang }: { t: T; lang: Lang }) {
  const { data, error } = useLoad<StatsView>(system.stats, 10_000)
  if (!data) return <Loading error={error} />
  const now = Date.now()
  const ranking = usageRanking(data.stats.providers)
  const c = data.stats.counters
  const lastRun = data.fidelity[data.fidelity.length - 1]
  const prevRun = data.fidelity[data.fidelity.length - 2]

  return (
    <div className="sys">
      <div className="sys-grid">
        <Stat title={pick(lang, 'Desde', 'Since')} value={ago(data.stats.since, now, lang)} note={pick(lang, 'Las estadísticas empiezan aquí y se quedan en tu equipo.', 'Statistics start here and stay on your computer.')} />
        <Stat title={pick(lang, 'Arranques', 'Starts')} value={c['provider.start'] ?? 0} />
        <Stat title={pick(lang, 'Laboratorios verificados', 'Labs verified')} value={c['lab.verify'] ?? 0} note={pick(lang, `${c['lab.complete'] ?? 0} completados`, `${c['lab.complete'] ?? 0} completed`)} />
        <Stat
          title={pick(lang, 'Fidelidad', 'Fidelity')}
          value={lastRun ? `${Math.round((lastRun.passed / Math.max(1, lastRun.steps)) * 100)} %` : '—'}
          note={lastRun ? ago(lastRun.at, now, lang) : pick(lang, 'Aún no has ejecutado la matriz.', 'You have not run the matrix yet.')}
        />
      </div>

      {ranking.length === 0 ? (
        <EmptyState title={pick(lang, 'Tiempo en marcha por emulador', 'Time up per emulator')} body={pick(lang, 'Arranca algún emulador y aquí verás cuánto lo usas.', 'Start an emulator and you will see how much you use it here.')} />
      ) : (
        <Card title={pick(lang, 'Tiempo en marcha por emulador', 'Time up per emulator')}>
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
        </Card>
      )}

      {lastRun && (
        <Card title={pick(lang, 'Fidelidad a lo largo del tiempo', 'Fidelity over time')}>
          <div className="spark" role="img" aria-label={pick(lang, 'Porcentaje de pasos superados en cada ejecución', 'Share of steps passed in each run')}>
            {data.fidelity.map((r) => {
              const pct = Math.round((r.passed / Math.max(1, r.steps)) * 100)
              return <i key={r.at} style={{ height: `${Math.max(6, pct)}%` }} title={`${pct} % · ${ago(r.at, now, lang)}`} />
            })}
          </div>
          {prevRun && <Regressions prev={prevRun.suites} cur={lastRun.suites} lang={lang} />}
        </Card>
      )}

      {data.journal.length === 0 ? (
        <EmptyState title={pick(lang, 'Diario', 'Journal')} body={pick(lang, 'Nada todavía.', 'Nothing yet.')} />
      ) : (
        <Card title={pick(lang, 'Diario', 'Journal')}>
          <ul className="journal">
            {data.journal.map((e, i) => (
              <li key={`${e.at}-${i}`}>
                <span>{describeEntry(e, lang)}</span>
                <time dateTime={new Date(e.at).toISOString()}>{ago(e.at, now, lang)}</time>
              </li>
            ))}
          </ul>
        </Card>
      )}
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
  const vars = useLoad<VariablesView>(variables.list)
  const persistable = providers.filter((p) => p.persistable)

  return (
    <div className="sys">
      <Card title={pick(lang, 'Preferencias', 'Preferences')}>
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
      </Card>

      <Card title={pick(lang, 'Datos de los emuladores', 'Emulator data')}>
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
      </Card>

      <VariablesCard lang={lang} view={vars.data} error={vars.error} providers={providers} reload={() => { vars.reload(); onChanged() }} />

      <Card title={pick(lang, 'Carpeta de datos', 'Data folder')}>
        {info.data ? (
          <>
            <p><code>{info.data.location ?? pick(lang, '(en memoria)', '(in memory)')}</code></p>
            <Facts items={Object.entries(info.data.files).map(([name, size]): [string, string] => [name, formatBytes(size)])} />
          </>
        ) : <Loading error={info.error} />}
        <BackupControls lang={lang} onChanged={() => { info.reload(); onChanged() }} />
      </Card>
    </div>
  )
}

/** The settings of every emulator: what it can be given, where each value comes from, and the actions to set, show, generate, delete and apply them. */
function VariablesCard({ lang, view, error, providers, reload }: { lang: Lang; view: VariablesView | null; error: string; providers: Provider[]; reload: () => void }) {
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [shown, setShown] = useState<Record<string, string>>({})
  const [notes, setNotes] = useState<Record<string, string>>({})
  const act = useAction()
  const title = pick(lang, 'Variables de los emuladores', 'Emulator variables')
  if (!view) return <Card title={title}><Loading error={error} /></Card>

  const state = (id: string) => providers.find((p) => p.id === id)?.state
  const reveal = (name: string) => act.run('', async () => { const r = await variables.reveal(name); setShown((s) => ({ ...s, [name]: r.value })) })
  const hide = (name: string) => setShown((s) => { const { [name]: _gone, ...rest } = s; return rest })

  return (
    <Card title={title}>
      <p className="muted">
        {view.available
          ? pick(lang, 'Contraseñas, usuarios y claves de cada emulador, guardadas cifradas con tu usuario de Windows. Una variable de entorno con el mismo nombre tiene prioridad. Los valores no se muestran hasta que pulsas «Mostrar».', 'Passwords, users and keys of each emulator, stored encrypted for your Windows user. An environment variable with the same name takes priority. Values are not shown until you press "Show".')
          : view.reason}
      </p>
      <div className="vars">
        {view.providers.map((p) => (
          <details key={p.provider} className="var-group" open={p.vars.some((v) => v.required && !v.set)}>
            <summary>
              <b>{p.name}</b>
              <span className="muted">
                {p.vars.length === 0 ? pick(lang, 'sin variables', 'no variables') : pick(lang, `${p.vars.filter((v) => v.set).length} de ${p.vars.length} con valor`, `${p.vars.filter((v) => v.set).length} of ${p.vars.length} set`)}
              </span>
              {p.vars.some((v) => v.required && !v.set) && <Pill tone="warn">{pick(lang, 'falta configurar', 'needs setup')}</Pill>}
            </summary>
            {p.vars.length === 0 ? (
              <p className="muted">{NO_VARS_NOTE[lang]}</p>
            ) : (
              <>
                <div className="actions">
                  <button disabled={act.busy || !view.available} onClick={() => act.run('', async () => { const r = await variables.init(p.provider); reload(); return r.created.length ? pick(lang, `Inicializadas: ${r.created.join(', ')}.`, `Initialized: ${r.created.join(', ')}.`) : pick(lang, 'Nada que inicializar: lo necesario ya tiene valor.', 'Nothing to initialize: what is needed already has a value.') })}>
                    {pick(lang, 'Inicializar lo necesario', 'Initialize what is needed')}
                  </button>
                  <button className="ghost" disabled={act.busy || !view.available} onClick={() => act.run('', async () => { const r = await variables.init(p.provider, true); reload(); return r.created.length ? pick(lang, `Inicializadas: ${r.created.join(', ')}.`, `Initialized: ${r.created.join(', ')}.`) : pick(lang, 'Todo tiene ya un valor.', 'Everything has a value already.') })}>
                    {pick(lang, 'Inicializar también las opcionales', 'Initialize the optional ones too')}
                  </button>
                  {state(p.provider) && state(p.provider) !== 'not_created' && (
                    <span title={pick(lang, 'Recrea el contenedor con los valores actuales; sus datos guardados se conservan.', 'Recreates the container with the current values; its saved data is kept.')}>
                      <ConfirmButton
                        lang={lang}
                        className="ghost"
                        disabled={act.busy}
                        label={state(p.provider) === 'running' ? pick(lang, 'Aplicar al emulador en marcha', 'Apply to the running emulator') : pick(lang, 'Preparar el próximo arranque', 'Prepare the next start')}
                        question={pick(lang, `Se recreará el contenedor de ${p.name} para usar los valores actuales. ¿Continuar?`, `The ${p.name} container will be recreated to use the current values. Continue?`)}
                        confirmLabel={pick(lang, 'Recrear', 'Recreate')}
                        onConfirm={() => act.run(pick(lang, 'Recreando el contenedor…', 'Recreating the container…'), async () => { const r = await variables.apply(p.provider); reload(); if (r.note) setNotes((n) => ({ ...n, [p.provider]: r.note! })); return r.applied ? pick(lang, 'Aplicado: el emulador se ha reiniciado con los valores nuevos.', 'Applied: the emulator restarted with the new values.') : pick(lang, 'El contenedor se ha quitado; el próximo arranque usará los valores nuevos.', 'The container was removed; the next start will use the new values.') })}
                      />
                    </span>
                  )}
                </div>
                {notes[p.provider] && <Notice tone="warn">{notes[p.provider]}</Notice>}
                <ul className="secrets">
                  {p.vars.map((v) => {
                    const help = VAR_HELP[v.name]
                    return (
                      <li key={v.name}>
                        <div>
                          <b>{help?.label[lang] ?? v.name}</b> <code>{v.name}</code>
                          <small>
                            {v.required ? pick(lang, 'Obligatoria', 'Required') : pick(lang, 'Opcional', 'Optional')} · {v.source === 'environment' ? pick(lang, 'viene del entorno', 'from the environment') : v.source === 'stored' ? pick(lang, 'guardada', 'stored') : pick(lang, 'sin valor', 'not set')}
                            {v.fixedAtCreation && ` · ${pick(lang, 'solo al crear los datos', 'only when data is first created')}`}
                          </small>
                          {help && <small>{help.help[lang]}</small>}
                        </div>
                        <span className="mask">{shown[v.name] === undefined ? maskSecret(v.set) || '—' : ''}</span>
                        {shown[v.name] !== undefined && (
                          <div className="secret-row">
                            <code className="secret-once">{shown[v.name]}</code>
                            <CopyButton lang={lang} text={shown[v.name]} className="" />
                            <button className="ghost" onClick={() => hide(v.name)}>{pick(lang, 'Ocultar', 'Hide')}</button>
                          </div>
                        )}
                        <div className="actions">
                          <input
                            type={v.secret ? 'password' : 'text'}
                            autoComplete="off"
                            placeholder={v.default ? `${pick(lang, 'Por ejemplo', 'For example')}: ${v.default}` : pick(lang, 'Nuevo valor', 'New value')}
                            value={draft[v.name] ?? ''}
                            disabled={!view.available}
                            aria-label={`${help?.label[lang] ?? v.name}: ${pick(lang, 'nuevo valor', 'new value')}`}
                            onChange={(e) => setDraft((d) => ({ ...d, [v.name]: e.target.value }))}
                          />
                          <button disabled={act.busy || !(draft[v.name] ?? '') || !view.available} onClick={() => act.run('', async () => { await system.setSecret(v.name, draft[v.name]); setDraft((d) => ({ ...d, [v.name]: '' })); hide(v.name); reload(); return pick(lang, 'Guardado.', 'Saved.') })}>{pick(lang, 'Guardar', 'Save')}</button>
                          {v.canGenerate && view.available && (
                            <button className="ghost" disabled={act.busy} onClick={() => act.run('', async () => { const r = await system.generateSecret(v.name); setShown((s) => ({ ...s, [v.name]: r.value })); reload() })}>{pick(lang, 'Generar', 'Generate')}</button>
                          )}
                          {v.set && <button className="ghost" disabled={act.busy} onClick={() => reveal(v.name)}>{pick(lang, 'Mostrar', 'Show')}</button>}
                          {v.source === 'stored' && view.available && <button className="ghost danger" disabled={act.busy} onClick={() => act.run('', async () => { await system.deleteSecret(v.name); hide(v.name); reload() })}>{pick(lang, 'Borrar', 'Delete')}</button>}
                        </div>
                      </li>
                    )
                  })}
                </ul>
              </>
            )}
          </details>
        ))}
      </div>
      <Feedback working={act.working} error={act.error} done={act.done} />
    </Card>
  )
}

function BackupControls({ lang, onChanged }: { lang: Lang; onChanged: () => void }) {
  const act = useAction()
  const [what, setWhat] = useState<ResetWhat>('stats')
  // A backup that was picked and read, waiting for the explicit "restore".
  const [pending, setPending] = useState<{ name: string; bundle: Parameters<typeof system.importData>[0] } | null>(null)
  const file = useRef<HTMLInputElement>(null)

  const download = () => act.run('', async () => {
    const bundle = await system.exportData()
    const url = URL.createObjectURL(new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url; a.download = backupName(new Date()); a.click()
    URL.revokeObjectURL(url)
    return pick(lang, 'Copia descargada. No incluye contraseñas ni datos de emuladores.', 'Backup downloaded. It holds no passwords or emulator data.')
  })

  const choose = (f: File | undefined) => {
    if (!f) return
    setPending(null)
    act.run('', async () => {
      const parsed = parseBackup(await f.text())
      if (!parsed.ok) throw new Error(pick(lang, 'Ese archivo no es una copia de Nimbo.', 'That file is not a Nimbo backup.'))
      setPending({ name: f.name, bundle: parsed.bundle as Parameters<typeof system.importData>[0] })
    }).finally(() => { if (file.current) file.current.value = '' })
  }

  const restore = () => {
    if (!pending) return
    act.run(pick(lang, 'Restaurando…', 'Restoring…'), async () => {
      const r = await system.importData(pending.bundle)
      setPending(null)
      onChanged()
      return pick(lang, `Restaurado: ${r.restored.join(', ')}. Recarga la página para ver todos los cambios.`, `Restored: ${r.restored.join(', ')}. Reload the page to see every change.`)
    })
  }

  const reset = () => act.run('', async () => { await system.reset(what); onChanged(); return pick(lang, 'Hecho.', 'Done.') })

  return (
    <div className="backup">
      <div className="actions">
        <button disabled={act.busy} onClick={download}>{pick(lang, 'Descargar copia', 'Download backup')}</button>
        <button className="ghost" disabled={act.busy} onClick={() => file.current?.click()}>{pick(lang, 'Restaurar copia…', 'Restore backup…')}</button>
        <input ref={file} type="file" accept="application/json,.json" hidden aria-label={pick(lang, 'Archivo de copia', 'Backup file')} onChange={(e) => choose(e.target.files?.[0])} />
      </div>
      {pending && (
        <div className="actions">
          <span className="muted">{pick(lang, 'Copia lista: ', 'Backup ready: ')}<code>{pending.name}</code></span>
          <ConfirmButton
            lang={lang}
            label={pick(lang, 'Restaurar esta copia', 'Restore this backup')}
            question={pick(lang, 'Restaurar sustituye tus ajustes, progreso y estadísticas actuales. ¿Continuar?', 'Restoring replaces your current settings, progress and statistics. Continue?')}
            confirmLabel={pick(lang, 'Restaurar', 'Restore')}
            disabled={act.busy}
            onConfirm={restore}
          />
          <button className="ghost" disabled={act.busy} onClick={() => setPending(null)}>{pick(lang, 'Descartar', 'Discard')}</button>
        </div>
      )}
      <div className="actions">
        <select value={what} onChange={(e) => setWhat(e.target.value as ResetWhat)} aria-label={pick(lang, 'Qué borrar', 'What to delete')}>
          <option value="stats">{pick(lang, 'Estadísticas y diario', 'Statistics and journal')}</option>
          <option value="history">{pick(lang, 'Historial de fidelidad', 'Fidelity history')}</option>
          <option value="progress">{pick(lang, 'Progreso de laboratorios', 'Lab progress')}</option>
          <option value="all">{pick(lang, 'Todo lo anterior', 'All of the above')}</option>
        </select>
        <ConfirmButton
          lang={lang}
          label={pick(lang, 'Borrar', 'Delete')}
          question={pick(lang, 'Esto borra datos de Nimbo y no se puede deshacer. ¿Continuar?', 'This deletes Nimbo data and cannot be undone. Continue?')}
          confirmLabel={pick(lang, 'Borrar', 'Delete')}
          disabled={act.busy}
          onConfirm={reset}
        />
      </div>
      <Feedback working={act.working} error={act.error} done={act.done} />
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
          <button className="primary" disabled={act.busy} onClick={() => act.run('', async () => { onStatus(await updatesApi.check(true)) })}>{act.busy ? '…' : pick(lang, 'Buscar ahora', 'Check now')}</button>
        </div>
        {status.error && <Notice tone="bad">{status.error}</Notice>}
        {!status.error && status.latest && !status.available && <Feedback done={pick(lang, 'Tienes la última versión.', 'You are up to date.')} />}
      </section>

      {status.available && status.latest && (
        <Card tone="highlight" title={pick(lang, `Nimbo ${status.latest.version} disponible`, `Nimbo ${status.latest.version} available`)}>
          <pre className="notes">{status.latest.notes.trim() || pick(lang, 'Sin notas.', 'No notes.')}</pre>

          {status.asset ? (
            <p className="muted"><code>{status.asset.name}</code> · {formatBytes(status.asset.size)}</p>
          ) : status.manual ? (
            <p>{pick(lang, 'Esta instalación no se actualiza sola. ', 'This installation does not update itself. ')}<code>{status.manual}</code></p>
          ) : null}

          <div className="actions">
            {status.asset && !installer && (
              <button className="primary" disabled={act.busy} onClick={() => act.run(pick(lang, 'Descargando y verificando…', 'Downloading and verifying…'), async () => { await updatesApi.download(status.latest!.version); await refresh(); return pick(lang, 'Descargada y verificada con SHA256SUMS.', 'Downloaded and verified against SHA256SUMS.') })}>
                {act.busy ? pick(lang, 'Descargando…', 'Downloading…') : pick(lang, 'Descargar y verificar', 'Download and verify')}
              </button>
            )}
            {installer && status.canInstall && (
              <ConfirmButton
                lang={lang}
                className="primary"
                label={pick(lang, 'Instalar ahora', 'Install now')}
                question={pick(lang, `Nimbo se cerrará, instalará la ${installer.version} y se abrirá solo. Tus emuladores seguirán en Docker. ¿Instalar?`, `Nimbo will close, install ${installer.version} and reopen by itself. Your emulators stay in Docker. Install?`)}
                confirmLabel={pick(lang, 'Instalar ahora', 'Install now')}
                disabled={act.busy}
                onConfirm={() => act.run('', async () => (await updatesApi.install(installer.version)).message)}
              />
            )}
          </div>
          {installer && (
            <Notice tone={installer.verified ? 'ok' : 'warn'}>
              {installer.verified ? pick(lang, 'Verificado con el SHA256SUMS de la versión.', 'Verified against the release SHA256SUMS.') : pick(lang, 'Sin verificar: no había una suma de comprobación publicada.', 'Not verified: no published checksum was available.')}
              {' '}<code title={installer.sha256}>{installer.sha256.slice(0, 16)}…</code>
              {!status.canInstall && <> {pick(lang, 'Archivo en la carpeta de datos, listo para usar.', 'File in the data folder, ready to use.')}</>}
            </Notice>
          )}
        </Card>
      )}

      <Card title={pick(lang, 'Instalar desde un archivo (sin internet)', 'Install from a file (no internet)')}>
        <p className="muted">{pick(lang, 'Indica la ruta de un instalador de Nimbo que ya tengas (por ejemplo, del kit sin conexión). Si hay un SHA256SUMS a su lado o escribes la suma, se comprueba.', 'Give the path of a Nimbo installer you already have (for example from the offline kit). If a SHA256SUMS sits next to it, or you type the checksum, it is verified.')}</p>
        <div className="actions wide">
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="D:\\nimbo-kit\\Nimbo-0.3.0-windows-x64.msi" aria-label={pick(lang, 'Ruta del instalador', 'Installer path')} />
          <input value={sha} onChange={(e) => setSha(e.target.value)} placeholder={pick(lang, 'SHA-256 esperado (opcional)', 'Expected SHA-256 (optional)')} aria-label="SHA-256" />
          <button disabled={act.busy || !path.trim()} onClick={() => act.run('', async () => { const f = await updatesApi.importFile(path.trim(), sha.trim()); await refresh(); return pick(lang, `Importada la ${f.version}${f.verified ? ' (verificada)' : ' (sin verificar)'}.`, `Imported ${f.version}${f.verified ? ' (verified)' : ' (not verified)'}.`) })}>
            {pick(lang, 'Importar', 'Import')}
          </button>
        </div>
      </Card>

      {status.files.length > 0 && (
        <Card title={pick(lang, 'Descargadas', 'Downloaded')}>
          <ul className="journal">
            {status.files.map((f) => (
              <li key={f.name}><span><code>{f.name}</code> · {formatBytes(f.size)}</span><b>{f.verified ? pick(lang, 'verificada', 'verified') : pick(lang, 'sin verificar', 'not verified')}</b></li>
            ))}
          </ul>
          <div className="actions"><button className="ghost danger" disabled={act.busy} onClick={() => act.run('', async () => { await updatesApi.clean(); await refresh() })}>{pick(lang, 'Borrar descargas', 'Delete downloads')}</button></div>
        </Card>
      )}
      <Feedback working={act.working} error={act.error} done={act.done} />
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
  if (!data) return <Loading error={error} />
  const present = data.images.filter((i) => i.present).length
  const big = pick(lang, 'Trabajando… un paquete grande puede tardar varios minutos.', 'Working… a big pack can take several minutes.')

  return (
    <div className="sys">
      <section className="sys-card">
        <h3>{pick(lang, 'Imágenes en este equipo', 'Images on this computer')}: <b>{present} / {data.images.length}</b></h3>
        {!data.docker && <Notice tone="bad">{pick(lang, 'Docker no responde, así que no se puede saber qué imágenes hay.', 'Docker is not responding, so the images cannot be listed.')}</Notice>}
        <table className="sys-table">
          <thead><tr><th>{pick(lang, 'Emulador', 'Emulator')}</th><th>{pick(lang, 'Imagen', 'Image')}</th><th>{pick(lang, 'Paquete', 'Pack')}</th><th /></tr></thead>
          <tbody>
            {data.images.map((i) => (
              <tr key={i.provider}>
                <td>{i.provider}</td>
                <td><code>{i.image}</code></td>
                <td>{i.pack ?? '—'}</td>
                <td>{i.present ? <b className="okc">✓</b> : (
                  <button className="ghost small" disabled={act.busy || !data.docker} onClick={() => act.run(pick(lang, `Descargando la imagen de ${i.provider}…`, `Pulling the ${i.provider} image…`), async () => { await imagesApi.pull(i.provider); reload(); return pick(lang, `Descargada la imagen de ${i.provider}.`, `Pulled the ${i.provider} image.`) })}>{pick(lang, 'Descargar', 'Pull')}</button>
                )}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <Card title={pick(lang, 'Paquetes de imágenes', 'Image packs')}>
        <p className="muted">{pick(lang, 'Busco en: ', 'Looking in: ')}{data.folders.map((f) => <code key={f} className="folder">{f}</code>)}</p>
        {data.files.length === 0 ? (
          <p className="muted">{pick(lang, 'No hay paquetes en esas carpetas. Copia ahí los archivos del kit o escribe una ruta abajo.', 'There are no packs in those folders. Copy the kit files there or type a path below.')}</p>
        ) : (
          <ul className="journal">
            {data.files.map((f) => (
              <li key={f.path}>
                <span><code>{f.name}</code> · {formatBytes(f.size)} · {f.verified === true ? pick(lang, 'verificado', 'verified') : f.verified === false ? pick(lang, 'NO COINCIDE', 'DOES NOT MATCH') : pick(lang, 'sin suma', 'no checksum')}</span>
                <button className="small" disabled={act.busy || !data.docker || f.verified === false} onClick={() => act.run(big, async () => { const r = await imagesApi.load(f.path); reload(); return pick(lang, `Cargadas ${r.loaded.length} imágenes.`, `Loaded ${r.loaded.length} images.`) })}>{pick(lang, 'Cargar', 'Load')}</button>
              </li>
            ))}
          </ul>
        )}
        <div className="actions wide">
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="D:\\nimbo-kit\\images\\Nimbo-0.3.0-images-data.tar.gz" aria-label={pick(lang, 'Ruta del paquete', 'Pack path')} />
          <button disabled={act.busy || !path.trim() || !data.docker} onClick={() => act.run(big, async () => { const r = await imagesApi.load(path.trim()); reload(); return pick(lang, `Cargadas ${r.loaded.length} imágenes.`, `Loaded ${r.loaded.length} images.`) })}>{pick(lang, 'Cargar archivo', 'Load file')}</button>
        </div>
        <Feedback working={act.working} error={act.error} done={act.done} />
      </Card>

      <Card title={pick(lang, 'Preparar un kit sin conexión', 'Prepare an offline kit')}>
        <p className="muted">{pick(lang, 'En un equipo con internet, ejecuta esto una vez; después copia la carpeta al equipo sin conexión, instala el .msi y carga los paquetes desde aquí.', 'On a computer with internet, run this once; then copy the folder to the offline computer, install the .msi and load the packs from here.')}</p>
        <pre className="notes">powershell -File packaging\make-offline-kit.ps1 -Version &lt;x.y.z&gt; -Out D:\nimbo-kit</pre>
      </Card>
    </div>
  )
}
