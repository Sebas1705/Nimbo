import { useEffect, useRef, useState } from 'react'
import { registry, type ComposeDef, type DockerStatus, type SourceDef } from '../api'
import type { Lang } from '../i18n'
import {
  changedFiles,
  composeSummary,
  composeTone,
  composeToDraft,
  emptyComposeDraft,
  fileRefLabel,
  fileStatusLabel,
  fileTone,
  missingFiles,
  shortPath,
  upBlocker,
  type ComposeDraft,
} from '../registryLogic'
import { ago } from '../systemLogic'
import { ConfirmButton, CopyButton, EmptyState, Feedback, Notice, Pill, pick, useAction, useLoad } from '../ui'
import { ComposeForm } from './forms'
import { LoadState, SectionHead, type Load } from './parts'

export function ComposeSection({ lang, docker, load, sources, reload, goSources }: { lang: Lang; docker: DockerStatus | null; load: Load<ComposeDef[]>; sources: SourceDef[]; reload: () => void; goSources: () => void }) {
  const [editing, setEditing] = useState<ComposeDraft | null>(null)
  const list = load.data ?? []

  return (
    <section className="reg-section" aria-labelledby="reg-compose">
      <SectionHead id="reg-compose" title={pick(lang, 'Perfiles de compose', 'Compose profiles')} addLabel={list.length > 0 && !editing ? pick(lang, '+ Crear un perfil', '+ Create a profile') : undefined} onAdd={() => setEditing(emptyComposeDraft())} />
      {editing && <ComposeForm lang={lang} draft={editing} sources={sources} ids={list.map((c) => c.id)} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} goSources={goSources} />}
      <LoadState load={load} />
      {load.data && list.length === 0 && !editing && (
        <EmptyState
          title={pick(lang, 'Aún no hay perfiles de compose', 'No compose profiles yet')}
          body={pick(lang, 'Un perfil recuerda qué archivos docker-compose (y qué perfiles y .env) forman un entorno, por ejemplo "pruebas de la API". Lo subes y lo bajas con un botón, ves qué servicios corren y te avisamos si algún archivo cambió desde la última vez.', 'A profile remembers which docker-compose files (and which profiles and .env) make up an environment, for example "API tests". You raise and lower it with a button, see which services run, and we warn you if a file changed since last time.')}
          action={pick(lang, 'Crear mi primer perfil', 'Create my first profile')}
          onAction={() => setEditing(emptyComposeDraft())}
        />
      )}
      <div className="presets reg-wide">
        {list.map((c) => (
          <ComposeCard key={c.id} lang={lang} def={c} dockerUp={docker?.available === true} reload={reload} onEdit={() => setEditing(composeToDraft(c))} />
        ))}
      </div>
    </section>
  )
}

type ComposePanel = null | 'file' | 'logs'

function ComposeCard({ lang, def, dockerUp, reload, onEdit }: { lang: Lang; def: ComposeDef; dockerUp: boolean; reload: () => void; onEdit: () => void }) {
  const act = useAction()
  const [panel, setPanel] = useState<ComposePanel>(null)
  const [volumes, setVolumes] = useState(false)
  const blocker = upBlocker(def, dockerUp, lang)
  const changed = changedFiles(def)
  const missing = missingFiles(def)
  const running = def.state.running > 0
  const toggle = (p: Exclude<ComposePanel, null>) => setPanel((cur) => (cur === p ? null : p))

  const validate = () =>
    act.run(pick(lang, 'Comprobando los archivos con docker compose…', 'Checking the files with docker compose…'), async () => {
      const r = await registry.compose.validate(def.id)
      if (!r.ok) throw new Error(r.message || pick(lang, 'Los archivos no son válidos.', 'The files are not valid.'))
      return r.message || pick(lang, 'Los archivos son válidos.', 'The files are valid.')
    })

  const up = () =>
    act.run(pick(lang, 'Subiendo el entorno… puede tardar varios minutos si hay que descargar imágenes.', 'Bringing the environment up… it can take several minutes if images must be downloaded.'), async () => {
      try {
        await registry.compose.up(def.id)
      } finally {
        reload()
      }
      return pick(lang, 'Entorno arriba.', 'Environment is up.')
    })

  const down = () =>
    act.run(pick(lang, 'Bajando el entorno…', 'Taking the environment down…'), async () => {
      const withVolumes = volumes
      try {
        await registry.compose.down(def.id, withVolumes)
      } finally {
        reload()
        setVolumes(false)
      }
      return withVolumes ? pick(lang, 'Entorno abajo y volúmenes borrados.', 'Environment down and volumes deleted.') : pick(lang, 'Entorno abajo.', 'Environment is down.')
    })

  const remove = () =>
    act.run(pick(lang, 'Borrando…', 'Deleting…'), async () => {
      try {
        await registry.compose.remove(def.id)
      } finally {
        reload()
      }
    })

  const statuses = [...def.fileStatus]
  const n = changed.length
  const changedText = running
    ? pick(lang, `${n === 1 ? 'Un archivo cambió' : `${n} archivos cambiaron`} desde el último "Subir". Vuelve a subir el perfil para aplicar los cambios.`, `${n === 1 ? 'A file changed' : `${n} files changed`} since the last "Up". Bring the profile up again to apply the changes.`)
    : pick(lang, `${n === 1 ? 'Un archivo cambió' : `${n} archivos cambiaron`} desde el último "Subir".`, `${n === 1 ? 'A file changed' : `${n} files changed`} since the last "Up".`)

  return (
    <article className="preset reg-compose" data-state={running ? 'running' : 'stopped'}>
      <header>
        <h3>{def.name}</h3>
        <Pill tone={composeTone(def)}>{composeSummary(def, lang)}</Pill>
      </header>
      <p className="reg-sub"><code>{def.id}</code>{def.purpose ? <> · {def.purpose}</> : null}</p>

      {def.state.services.length > 0 && (
        <ul className="chips" aria-label={pick(lang, 'Servicios', 'Services')}>
          {def.state.services.map((s) => (
            <li key={s.name} className={s.state === 'running' ? 'svc on' : 'svc'}>
              {s.name} · {s.state}{s.health ? ` · ${s.health}` : ''}
            </li>
          ))}
        </ul>
      )}

      <ul className="reg-files" aria-label={pick(lang, 'Archivos', 'Files')}>
        {def.files.map((f, i) => (
          <li key={i}>
            <code title={statuses[i]?.path}>{fileRefLabel(f)}</code>
            {statuses[i] && <Pill tone={fileTone(statuses[i].status)}>{fileStatusLabel(statuses[i].status, lang)}</Pill>}
          </li>
        ))}
        {def.envFile && (
          <li>
            <code title={def.envFileStatus?.path}>{fileRefLabel(def.envFile)}</code> <small className="hint">.env</small>
            {def.envFileStatus && <Pill tone={fileTone(def.envFileStatus.status)}>{fileStatusLabel(def.envFileStatus.status, lang)}</Pill>}
          </li>
        )}
      </ul>
      {def.profiles.length > 0 && (
        <p className="reg-sub">{pick(lang, 'Perfiles: ', 'Profiles: ')}{def.profiles.join(', ')}</p>
      )}
      {n > 0 && <Notice tone="warn">{changedText}</Notice>}
      {missing.length > 0 && <Notice tone="bad">{pick(lang, 'No se encuentra: ', 'Not found: ')}{missing.map(shortPath).join(', ')}</Notice>}
      {def.lastUpAt && <small className="hint">{pick(lang, 'Última subida', 'Last up')} {ago(def.lastUpAt, Date.now(), lang)}</small>}

      <Feedback working={act.working} error={act.error} done={act.done} />

      <div className="actions">
        <button className="primary" disabled={act.busy || !!blocker} title={blocker || undefined} onClick={up}>{pick(lang, 'Subir', 'Up')}</button>
        <ConfirmButton
          lang={lang}
          className="ghost"
          label={pick(lang, 'Bajar', 'Down')}
          question={pick(lang, 'Se pararán y eliminarán los contenedores de este perfil. Tus archivos no se tocan.', 'The containers of this profile will be stopped and removed. Your files are not touched.')}
          confirmLabel={pick(lang, 'Sí, bajar', 'Yes, take down')}
          disabled={act.busy || !running}
          onConfirm={down}
        >
          {/* Pressing the label text would move focus out of the question (to the page) and withdraw it before the click lands. */}
          <label className="check" onMouseDown={(e) => e.preventDefault()}><input type="checkbox" checked={volumes} onChange={(e) => setVolumes(e.target.checked)} /> {pick(lang, 'Borrar también los volúmenes (se pierden sus datos)', 'Also delete the volumes (their data is lost)')}</label>
        </ConfirmButton>
        <button className="ghost" disabled={act.busy || !dockerUp} onClick={validate}>{pick(lang, 'Validar', 'Validate')}</button>
        <button className="ghost" onClick={() => toggle('file')} aria-expanded={panel === 'file'}>{pick(lang, 'Ver archivo', 'View file')}</button>
        <button className="ghost" onClick={() => toggle('logs')} aria-expanded={panel === 'logs'}>Logs</button>
        <button className="ghost" disabled={act.busy} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <ConfirmButton
          lang={lang}
          label={pick(lang, 'Borrar', 'Delete')}
          question={pick(lang, `¿Borrar el perfil ${def.name}? Tus archivos de compose no se tocan.`, `Delete the profile ${def.name}? Your compose files are not touched.`)}
          confirmLabel={pick(lang, 'Sí, borrar', 'Yes, delete')}
          disabled={act.busy || running}
          onConfirm={remove}
        />
      </div>
      {blocker && <small className="hint">{blocker}</small>}
      {running && <small className="hint">{pick(lang, 'Para borrarlo, bájalo antes.', 'To delete it, take it down first.')}</small>}

      {panel === 'file' && <FileViewer lang={lang} def={def} />}
      {panel === 'logs' && <LogsViewer lang={lang} def={def} />}
    </article>
  )
}

function FileViewer({ lang, def }: { lang: Lang; def: ComposeDef }) {
  const [index, setIndex] = useState(0)
  const file = useLoad(() => registry.compose.file(def.id, index))
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return }
    file.reload()
  }, [index]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="reg-panel" role="group" aria-label={pick(lang, 'Contenido del archivo', 'File contents')}>
      {def.files.length > 1 && (
        <label className="rf-inline">{pick(lang, 'Archivo', 'File')}
          <select value={index} onChange={(e) => setIndex(Number(e.target.value))}>
            {def.files.map((f, i) => <option key={i} value={i}>{fileRefLabel(f)}</option>)}
          </select>
        </label>
      )}
      {file.error && <p className="err" role="alert">{file.error}</p>}
      {!file.data && !file.error && <p className="muted">…</p>}
      {file.data && (
        <>
          <div className="actions">
            <small className="hint">{pick(lang, 'Leído ahora mismo de ', 'Read just now from ')}<code>{file.data.path}</code></small>
            <CopyButton lang={lang} text={file.data.content} />
          </div>
          <pre className="reg-pre" tabIndex={0}>{file.data.content}</pre>
          {file.data.truncated && <small className="hint">{pick(lang, 'El archivo es grande: se muestra solo el principio.', 'The file is large: only the start is shown.')}</small>}
        </>
      )}
    </div>
  )
}

function LogsViewer({ lang, def }: { lang: Lang; def: ComposeDef }) {
  const [service, setService] = useState('')
  const logs = useLoad(() => registry.compose.logs(def.id, service, 200))
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return }
    logs.reload()
  }, [service]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="reg-panel" role="group" aria-label="Logs">
      <div className="actions">
        <label className="rf-inline">{pick(lang, 'Servicio', 'Service')}
          <select value={service} onChange={(e) => setService(e.target.value)}>
            <option value="">{pick(lang, 'Todos', 'All')}</option>
            {def.state.services.map((s) => <option key={s.name} value={s.name}>{s.name}</option>)}
          </select>
        </label>
        <button className="ghost small" onClick={logs.reload}>{pick(lang, 'Actualizar', 'Refresh')}</button>
        {logs.data && <CopyButton lang={lang} text={logs.data.lines} />}
      </div>
      {logs.error && <p className="err" role="alert">{logs.error}</p>}
      {!logs.data && !logs.error && <p className="muted">…</p>}
      {logs.data && <pre className="reg-pre" tabIndex={0}>{logs.data.lines.trim() || pick(lang, 'Sin líneas de log todavía.', 'No log lines yet.')}</pre>}
    </div>
  )
}
