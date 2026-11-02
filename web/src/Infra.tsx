import { useMemo, useState } from 'react'
import { api, groupByCloud, type DockerStatus, type Provider, type Stack, type StackList } from './api'
import { extraPorts, primaryPort } from './guideLogic'
import { guideFor, stackBlurb } from './guides'
import type { Key, Lang } from './i18n'

type T = (k: Key) => string

/** What a card can ask for: the container lifecycle, the "keep my data" switch, and deleting what was kept. */
export type ProviderAction = 'start' | 'stop' | 'remove' | 'persistOn' | 'persistOff' | 'deleteData' | 'initStart'

interface Props {
  t: T
  lang: Lang
  docker: DockerStatus | null
  providers: Provider[]
  stackList: StackList
  busy: Set<string>
  errors: Record<string, string>
  stackBusy: string | null
  stackErrors: Record<string, string>
  act: (p: Provider, action: ProviderAction) => void
  runStack: (s: Stack, action: 'stackUp' | 'stackDown') => void
  onPanel: (kind: 'logs' | 'env', p: Provider) => void
  onExport: (s: Stack) => void
  onGuide: (providerId: string) => void
  /** Called after a stack of your own was saved or deleted, so the list refreshes. */
  onStacksChanged: () => void
  /** Open the Registry with this own image ready to edit. */
  onRegistry: (imageId: string) => void
}

/** Stacks (groups that start together) on top, then every provider as a module with its port on the front plate. */
export function InfraView(props: Props) {
  const { t, lang, docker, providers, stackList, stackBusy, stackErrors, runStack, onExport } = props
  const groups = useMemo(() => groupByCloud(providers), [providers])
  const names = new Map(providers.map((p) => [p.id, p.name]))
  const [editing, setEditing] = useState<{ id: string; description: string; providers: string[]; isNew: boolean } | null>(null)
  const es = lang === 'es'

  return (
    <div className="infra">
      <section aria-label={t('stacks')}>
          <div className="section-head">
            <h2 className="eyebrow">{t('stacks')}</h2>
            <button className="ghost small" onClick={() => setEditing({ id: '', description: '', providers: [], isNew: true })}>
              {es ? '+ Crear un stack' : '+ Create a stack'}
            </button>
          </div>
          {editing && <StackForm lang={lang} providers={providers} draft={editing} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); props.onStacksChanged() }} />}
          <div className="presets">
            {stackList.stacks.map((s) => (
              <article key={s.id} className="preset" data-state={s.state}>
                <header>
                  <h3>{s.id}</h3>
                  <span className={`pill stack-${s.state}`}>
                    <i className={s.state === 'running' ? 'led on' : 'dot'} />
                    {t(`stack_${s.state}` as Key)} · {s.running}/{s.total}
                  </span>
                </header>
                <p>{stackBlurb(s.id, lang, s.description)}</p>
                <ul className="chips">
                  {s.providers.map((id) => (
                    <li key={id}>{names.get(id) ?? id}</li>
                  ))}
                </ul>
                {stackErrors[s.id] && <p className="err">{stackErrors[s.id]}</p>}
                <div className="actions">
                  <button className="primary" disabled={stackBusy !== null || !docker?.available || s.state === 'running'} onClick={() => runStack(s, 'stackUp')}>
                    {stackBusy === s.id ? '…' : t('up')}
                  </button>
                  <button className="ghost" disabled={stackBusy !== null || s.running === 0} onClick={() => runStack(s, 'stackDown')}>
                    {t('down')}
                  </button>
                  <button className="ghost" onClick={() => onExport(s)}>
                    {t('export')}
                  </button>
                  {s.custom && (
                    <>
                      <button className="ghost" onClick={() => setEditing({ id: s.id, description: s.description, providers: s.providers, isNew: false })}>
                        {es ? 'Editar' : 'Edit'}
                      </button>
                      <button
                        className="ghost danger"
                        disabled={stackBusy !== null}
                        onClick={async () => {
                          if (!window.confirm(es ? `¿Borrar el stack ${s.id}? Los emuladores no se tocan.` : `Delete the stack ${s.id}? The emulators are not touched.`)) return
                          try {
                            await api.deleteStack(s.id)
                          } finally {
                            props.onStacksChanged()
                          }
                        }}
                      >
                        {es ? 'Borrar' : 'Delete'}
                      </button>
                    </>
                  )}
                </div>
              </article>
            ))}
          </div>
          {stackList.stacks.length === 0 && <p className="muted">{es ? 'Aún no hay stacks. Crea uno con los emuladores que quieras levantar juntos.' : 'No stacks yet. Create one with the emulators you want to bring up together.'}</p>}
          {stackList.problems.length > 0 && (
            <div className="banner" role="alert">
              <b>{t('problems')}</b>
              <ul>
                {stackList.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </div>
          )}
        </section>

      {providers.length === 0 && <p className="muted">{t('loading')}</p>}

      {groups.map(([cloud, list]) => (
        <section key={cloud} className={`cloud cloud-${cloud}`}>
          <h2 className="eyebrow cloud-title">{t(`cloud_${cloud}` as Key)}</h2>
          <div className="modules">
            {list.map((p) => (
              <Module key={p.id} p={p} {...props} />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function Module({ p, t, lang, docker, busy, errors, act, onPanel, onGuide, onRegistry }: Props & { p: Provider }) {
  const port = primaryPort(p)
  const more = extraPorts(p)
  const blurb = guideFor(p.id)?.blurb[lang] ?? p.description

  return (
    <article className="module" data-state={p.state}>
      <header>
        <h3>
          {p.name}
          {p.custom && <span className="own-badge" title={lang === 'es' ? 'Imagen propia del Registro' : 'Own image from the Registry'}>{lang === 'es' ? 'propia' : 'own'}</span>}
        </h3>
        <span className={`pill ${p.state}`}>
          <i className={p.state === 'running' ? 'led on' : 'dot'} />
          {t(`state_${p.state}` as Key)}
        </span>
      </header>

      <div className="plate" aria-label={`${t('portLabel')} ${port ?? ''}`}>
        <span className="plate-port">{port ?? '—'}</span>
        <span className="plate-label">{more.length > 0 ? `${t('portLabel')} · +${more.length}` : t('portLabel')}</span>
      </div>

      <p className="blurb">{blurb}</p>
      <p className="image">
        <code>{p.image}</code>
      </p>

      {p.persistable && (
        <div className="keep">
          <label>
            <input type="checkbox" checked={p.persist === true} disabled={busy.has(p.id)} onChange={(e) => act(p, e.target.checked ? 'persistOn' : 'persistOff')} />
            <span>{lang === 'es' ? 'Conservar datos' : 'Keep data'}</span>
          </label>
          {p.persist && p.state === 'running' && (
            <small>{lang === 'es' ? 'Se aplica al arrancar de nuevo.' : 'Applies the next time it starts.'}</small>
          )}
          {p.hasData && (
            <button className="ghost danger small" disabled={busy.has(p.id) || p.state === 'running'} title={p.state === 'running' ? (lang === 'es' ? 'Párala antes' : 'Stop it first') : undefined} onClick={() => act(p, 'deleteData')}>
              {lang === 'es' ? 'Borrar datos guardados' : 'Delete saved data'}
            </button>
          )}
        </div>
      )}

      {p.state !== 'running' && (p.needsSetup?.length ?? 0) > 0 && (
        <p className="setup">
          {lang === 'es' ? 'Necesita credenciales antes de arrancar: ' : 'It needs credentials before it can start: '}
          <code>{p.needsSetup!.join(', ')}</code>
        </p>
      )}
      {errors[p.id] && <p className="err">{errors[p.id]}</p>}
      <div className="actions">
        {p.state === 'running' ? (
          <button disabled={busy.has(p.id)} onClick={() => act(p, 'stop')}>
            {t('stop')}
          </button>
        ) : (
          <button className="primary" disabled={busy.has(p.id) || !docker?.available} onClick={() => act(p, (p.needsSetup?.length ?? 0) > 0 ? 'initStart' : 'start')}>
            {(p.needsSetup?.length ?? 0) > 0 ? (lang === 'es' ? 'Inicializar y arrancar' : 'Initialize and start') : t('start')}
          </button>
        )}
        {p.custom ? (
          <button className="ghost" onClick={() => onRegistry(p.id)}>
            {lang === 'es' ? 'Editar en el Registro' : 'Edit in the Registry'}
          </button>
        ) : (
          <button className="ghost" onClick={() => onGuide(p.id)}>
            {t('guideBtn')}
          </button>
        )}
        <button className="ghost" disabled={p.state === 'not_created'} onClick={() => onPanel('logs', p)}>
          {t('logs')}
        </button>
        <button className="ghost" onClick={() => onPanel('env', p)}>
          {t('env')}
        </button>
        {p.state === 'stopped' && (
          <button className="ghost danger" disabled={busy.has(p.id)} onClick={() => act(p, 'remove')}>
            {t('remove')}
          </button>
        )}
      </div>
    </article>
  )
}

/** Create or edit a stack of your own: a name, a sentence, and the emulators that start together. */
function StackForm({ lang, providers, draft, onCancel, onSaved }: { lang: Lang; providers: Provider[]; draft: { id: string; description: string; providers: string[]; isNew: boolean }; onCancel: () => void; onSaved: () => void }) {
  const es = lang === 'es'
  const [id, setId] = useState(draft.id)
  const [description, setDescription] = useState(draft.description)
  const [picked, setPicked] = useState<string[]>(draft.providers)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const toggle = (pid: string) => setPicked((cur) => (cur.includes(pid) ? cur.filter((x) => x !== pid) : [...cur, pid]))

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.saveStack(id.trim(), description.trim(), picked)
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="stack-form" role="group" aria-label={es ? 'Crear un stack' : 'Create a stack'}>
      <label>
        {es ? 'Nombre' : 'Name'}
        <input value={id} disabled={!draft.isNew} placeholder="mi-backend" onChange={(e) => setId(e.target.value.toLowerCase())} />
      </label>
      <label>
        {es ? 'Descripción (opcional)' : 'Description (optional)'}
        <input value={description} maxLength={200} onChange={(e) => setDescription(e.target.value)} />
      </label>
      <fieldset>
        <legend>{es ? 'Emuladores, en el orden en que los eliges' : 'Emulators, in the order you pick them'}</legend>
        <div className="pick-grid">
          {providers.map((p) => (
            <label key={p.id} className={picked.includes(p.id) ? 'pick on' : 'pick'}>
              <input type="checkbox" checked={picked.includes(p.id)} onChange={() => toggle(p.id)} />
              {p.name}
              {picked.includes(p.id) && <small>{picked.indexOf(p.id) + 1}</small>}
            </label>
          ))}
        </div>
      </fieldset>
      {error && <p className="err" role="alert">{error}</p>}
      <div className="actions">
        <button className="primary" disabled={busy || !id.trim() || picked.length === 0} onClick={save}>
          {es ? 'Guardar stack' : 'Save stack'}
        </button>
        <button className="ghost" onClick={onCancel}>
          {es ? 'Cancelar' : 'Cancel'}
        </button>
      </div>
    </div>
  )
}
