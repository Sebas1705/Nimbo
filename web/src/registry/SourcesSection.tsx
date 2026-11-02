import { useState } from 'react'
import { registry, type ComposeDef, type SourceDef } from '../api'
import type { Lang } from '../i18n'
import { emptySourceDraft, sourceDetail, sourceStatusLabel, sourceTone, syncLabel, type SourceDraft } from '../registryLogic'
import { ago } from '../systemLogic'
import { ConfirmButton, EmptyState, Feedback, Pill, pick, useAction } from '../ui'
import { SourceForm } from './forms'
import { LoadState, SectionHead, type Load } from './parts'

export function SourcesSection({ lang, load, reload, composes }: { lang: Lang; load: Load<SourceDef[]>; reload: () => void; composes: ComposeDef[] }) {
  const [editing, setEditing] = useState<SourceDraft | null>(null)
  const list = load.data ?? []
  const usedBy = (id: string) => composes.filter((c) => c.files.some((f) => f.source === id) || c.envFile?.source === id).map((c) => c.name || c.id)

  return (
    <section className="reg-section" id="reg-sources" aria-labelledby="reg-sources-h">
      <SectionHead id="reg-sources-h" title={pick(lang, 'Repositorios', 'Repositories')} addLabel={list.length > 0 && !editing ? pick(lang, '+ Enlazar un repositorio', '+ Link a repository') : undefined} onAdd={() => setEditing(emptySourceDraft())} />
      {editing && <SourceForm lang={lang} draft={editing} ids={list.map((s) => s.id)} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      <LoadState load={load} />
      {load.data && list.length === 0 && !editing && (
        <EmptyState
          title={pick(lang, 'Aún no hay repositorios enlazados', 'No repositories linked yet')}
          body={pick(lang, 'Tus archivos de compose y .env suelen vivir en un repositorio. Enlaza una carpeta de tu equipo (se lee tal cual, sin copiarla) o una dirección de Git (Nimbo la clona y la mantiene al día). Luego los perfiles de compose pueden apuntar a archivos de dentro.', 'Your compose and .env files usually live in a repository. Link a folder on your computer (read as it is, never copied) or a Git address (Nimbo clones it and keeps it up to date). Compose profiles can then point at files inside.')}
          action={pick(lang, 'Enlazar mi primer repositorio', 'Link my first repository')}
          onAction={() => setEditing(emptySourceDraft())}
        />
      )}
      <div className="presets">
        {list.map((s) => (
          <SourceCard key={s.id} lang={lang} src={s} usedBy={usedBy(s.id)} reload={reload} onEdit={() => setEditing({ id: s.id, kind: s.kind, path: s.path ?? '', url: s.url ?? '', branch: s.branch ?? '', isNew: false })} />
        ))}
      </div>
    </section>
  )
}

function SourceCard({ lang, src, usedBy, reload, onEdit }: { lang: Lang; src: SourceDef; usedBy: string[]; reload: () => void; onEdit: () => void }) {
  const act = useAction()
  const detail = sourceDetail(src, lang)

  const sync = () =>
    act.run(src.kind === 'git' ? pick(lang, 'Sincronizando con Git… puede tardar hasta un par de minutos.', 'Syncing with Git… this can take up to a couple of minutes.') : pick(lang, 'Mirando la carpeta…', 'Looking at the folder…'), async () => {
      try {
        await registry.sources.sync(src.id)
      } finally {
        reload()
      }
      return src.kind === 'git' ? pick(lang, 'Sincronizado.', 'Synced.') : pick(lang, 'Estado actualizado.', 'Status refreshed.')
    })

  const remove = () =>
    act.run(pick(lang, 'Quitando…', 'Removing…'), async () => {
      try {
        await registry.sources.remove(src.id)
      } finally {
        reload()
      }
    })

  const extra = src.kind === 'git' ? pick(lang, ' La copia clonada en la carpeta de datos de Nimbo se borrará.', ' The clone in Nimbo\'s data folder will be deleted.') : pick(lang, ' Tu carpeta no se toca.', ' Your folder is not touched.')

  return (
    <article className="preset" data-state={src.status === 'ok' ? 'running' : 'stopped'}>
      <header>
        <h3>{src.id}</h3>
        <Pill tone={sourceTone(src.status)}>{sourceStatusLabel(src.status, lang)}</Pill>
      </header>
      <p className="reg-sub">
        {src.kind === 'git' ? <>Git · <code>{src.url}</code>{src.branch ? <> · {src.branch}</> : null}</> : <>{pick(lang, 'Carpeta local', 'Local folder')} · <code>{src.path}</code></>}
      </p>
      {detail && <p>{detail}</p>}
      {src.kind === 'git' && src.dir && <small className="hint">{pick(lang, 'Copia en', 'Copy at')} <code>{src.dir}</code></small>}
      {src.status === 'not-cloned' && <p className="muted">{pick(lang, 'Todavía no se ha clonado. Pulsa "Clonar" para traerlo.', 'It has not been cloned yet. Press "Clone" to fetch it.')}</p>}
      {src.status === 'missing' && <p className="muted">{pick(lang, 'La carpeta no existe ahora mismo. Si la moviste, edita el repositorio con la ruta nueva.', 'The folder does not exist right now. If you moved it, edit the repository with the new path.')}</p>}
      {src.error && <p className="err">{src.error}</p>}
      {src.syncedAt && <small className="hint">{pick(lang, 'Revisado', 'Checked')} {ago(src.syncedAt, Date.now(), lang)}</small>}
      {usedBy.length > 0 && <small className="hint">{pick(lang, 'Lo usan: ', 'Used by: ')}{usedBy.join(', ')}</small>}
      <Feedback working={act.working} error={act.error} done={act.done} />
      <div className="actions">
        <button className="primary" disabled={act.busy} onClick={sync}>{syncLabel(src, lang)}</button>
        <button className="ghost" disabled={act.busy} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <ConfirmButton
          lang={lang}
          label={pick(lang, 'Quitar', 'Remove')}
          question={pick(lang, `¿Quitar el repositorio ${src.id}?${extra}`, `Remove the repository ${src.id}?${extra}`)}
          confirmLabel={pick(lang, 'Sí, quitar', 'Yes, remove')}
          disabled={act.busy}
          onConfirm={remove}
        />
      </div>
    </article>
  )
}
