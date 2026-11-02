import { useMemo, useState } from 'react'
import { api, groupByCloud, type DockerStatus, type Provider, type Stack, type StackList } from './api'
import { extraPorts, primaryPort, STACK_DESCRIPTION_MAX, stackErrors as validateStack } from './guideLogic'
import { guideFor, stackBlurb } from './guides'
import type { Key, Lang } from './i18n'
import './screens.css'
import { Card, ConfirmButton, EmptyState, Feedback, Field, FormActions, Notice, Pill, pick, useAction, useShown, type Tone } from './ui'

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

interface Draft {
  id: string
  description: string
  providers: string[]
  isNew: boolean
}

const stackTone = (state: Stack['state']): Tone => (state === 'running' ? 'ok' : state === 'partial' ? 'warn' : 'idle')
const moduleTone = (state: Provider['state']): Tone => (state === 'running' ? 'ok' : state === 'stopped' ? 'warn' : state === 'error' ? 'bad' : 'idle')

/** Stacks (groups that start together) on top, then every provider as a module with its port on the front plate. */
export function InfraView(props: Props) {
  const { t, lang, docker, providers, stackList, stackBusy, stackErrors, runStack, onExport } = props
  const groups = useMemo(() => groupByCloud(providers), [providers])
  const names = new Map(providers.map((p) => [p.id, p.name]))
  const [editing, setEditing] = useState<Draft | null>(null)
  const create = () => setEditing({ id: '', description: '', providers: [], isNew: true })

  return (
    <div className="infra">
      <section aria-label={t('stacks')}>
        <div className="section-head">
          <h2 className="eyebrow">{t('stacks')}</h2>
          <button className="ghost small" onClick={create}>
            {pick(lang, '+ Crear un stack', '+ Create a stack')}
          </button>
        </div>
        {editing && (
          <StackForm
            key={editing.isNew ? 'new' : editing.id}
            lang={lang}
            providers={providers}
            draft={editing}
            onCancel={() => setEditing(null)}
            onSaved={() => {
              setEditing(null)
              props.onStacksChanged()
            }}
          />
        )}
        <div className="presets">
          {stackList.stacks.map((s) => (
            <StackCard
              key={s.id}
              s={s}
              t={t}
              lang={lang}
              names={names}
              disabled={stackBusy !== null}
              starting={stackBusy === s.id}
              dockerUp={docker?.available === true}
              error={stackErrors[s.id]}
              onRun={runStack}
              onExport={onExport}
              onEdit={() => setEditing({ id: s.id, description: s.description, providers: s.providers, isNew: false })}
              onChanged={props.onStacksChanged}
            />
          ))}
        </div>
        {stackList.stacks.length === 0 && !editing && (
          <EmptyState
            title={pick(lang, 'Aún no hay stacks', 'No stacks yet')}
            body={pick(lang, 'Crea uno con los emuladores que quieras levantar juntos.', 'Create one with the emulators you want to bring up together.')}
            action={pick(lang, 'Crear un stack', 'Create a stack')}
            onAction={create}
          />
        )}
        {stackList.problems.length > 0 && (
          <Notice title={t('problems')}>
            <ul>
              {stackList.problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </Notice>
        )}
      </section>

      {providers.length === 0 && <Feedback working={t('loading')} />}

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

function StackCard({
  s, t, lang, names, disabled, starting, dockerUp, error, onRun, onExport, onEdit, onChanged,
}: {
  s: Stack
  t: T
  lang: Lang
  names: Map<string, string>
  /** Some stack is starting or stopping. */
  disabled: boolean
  starting: boolean
  dockerUp: boolean
  error?: string
  onRun: (s: Stack, action: 'stackUp' | 'stackDown') => void
  onExport: (s: Stack) => void
  onEdit: () => void
  onChanged: () => void
}) {
  const del = useAction()
  const remove = () =>
    del.run('', async () => {
      try {
        await api.deleteStack(s.id)
      } finally {
        onChanged()
      }
    })

  return (
    <Card
      title={s.id}
      tone={s.state === 'running' ? 'highlight' : undefined}
      actions={
        <Pill tone={stackTone(s.state)}>
          {t(`stack_${s.state}` as Key)} · {s.running}/{s.total}
        </Pill>
      }
    >
      <p>{stackBlurb(s.id, lang, s.description)}</p>
      <ul className="chips">
        {s.providers.map((id) => (
          <li key={id}>{names.get(id) ?? id}</li>
        ))}
      </ul>
      <Feedback error={error || del.error} />
      <div className="actions">
        <button className="primary" disabled={disabled || !dockerUp || s.state === 'running'} onClick={() => onRun(s, 'stackUp')}>
          {starting ? '…' : t('up')}
        </button>
        <button className="ghost" disabled={disabled || s.running === 0} onClick={() => onRun(s, 'stackDown')}>
          {t('down')}
        </button>
        <button className="ghost" onClick={() => onExport(s)}>
          {t('export')}
        </button>
        {s.custom && (
          <>
            <button className="ghost" onClick={onEdit}>
              {pick(lang, 'Editar', 'Edit')}
            </button>
            <ConfirmButton
              lang={lang}
              label={pick(lang, 'Borrar', 'Delete')}
              question={pick(lang, `¿Borrar el stack ${s.id}? Los emuladores no se tocan.`, `Delete the stack ${s.id}? The emulators are not touched.`)}
              confirmLabel={pick(lang, 'Sí, borrar', 'Yes, delete')}
              disabled={disabled || del.busy}
              onConfirm={remove}
            />
          </>
        )}
      </div>
    </Card>
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
          {p.custom && <span className="own-badge" title={pick(lang, 'Imagen propia del Registro', 'Own image from the Registry')}>{pick(lang, 'propia', 'own')}</span>}
        </h3>
        <Pill tone={moduleTone(p.state)}>{t(`state_${p.state}` as Key)}</Pill>
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
            <span>{pick(lang, 'Conservar datos', 'Keep data')}</span>
          </label>
          {p.persist && p.state === 'running' && <small>{pick(lang, 'Se aplica al arrancar de nuevo.', 'Applies the next time it starts.')}</small>}
          {p.hasData && (
            <span title={p.state === 'running' ? pick(lang, 'Párala antes', 'Stop it first') : undefined}>
              <ConfirmButton
                lang={lang}
                className="ghost danger small"
                label={pick(lang, 'Borrar datos guardados', 'Delete saved data')}
                question={pick(
                  lang,
                  `Se borrarán para siempre los datos guardados de ${p.name}. ¿Continuar?`,
                  `The saved data of ${p.name} will be deleted for good. Continue?`,
                )}
                confirmLabel={pick(lang, 'Sí, borrar', 'Yes, delete')}
                disabled={busy.has(p.id) || p.state === 'running'}
                onConfirm={() => act(p, 'deleteData')}
              />
            </span>
          )}
        </div>
      )}

      {p.state !== 'running' && (p.needsSetup?.length ?? 0) > 0 && (
        <p className="setup">
          {pick(lang, 'Necesita credenciales antes de arrancar: ', 'It needs credentials before it can start: ')}
          <code>{p.needsSetup!.join(', ')}</code>
        </p>
      )}
      <Feedback error={errors[p.id]} />
      <div className="actions">
        {p.state === 'running' ? (
          <button disabled={busy.has(p.id)} onClick={() => act(p, 'stop')}>
            {t('stop')}
          </button>
        ) : (
          <button className="primary" disabled={busy.has(p.id) || !docker?.available} onClick={() => act(p, (p.needsSetup?.length ?? 0) > 0 ? 'initStart' : 'start')}>
            {(p.needsSetup?.length ?? 0) > 0 ? pick(lang, 'Inicializar y arrancar', 'Initialize and start') : t('start')}
          </button>
        )}
        {p.custom ? (
          <button className="ghost" onClick={() => onRegistry(p.id)}>
            {pick(lang, 'Editar en el Registro', 'Edit in the Registry')}
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
function StackForm({ lang, providers, draft, onCancel, onSaved }: { lang: Lang; providers: Provider[]; draft: Draft; onCancel: () => void; onSaved: () => void }) {
  const [id, setId] = useState(draft.id)
  const [description, setDescription] = useState(draft.description)
  const [picked, setPicked] = useState<string[]>(draft.providers)
  const act = useAction()
  const errors = validateStack(lang, { id, description, providers: picked, isNew: draft.isNew })
  const sh = useShown(errors)
  const toggle = (pid: string) => {
    sh.touch('providers')
    setPicked((cur) => (cur.includes(pid) ? cur.filter((x) => x !== pid) : [...cur, pid]))
  }

  const save = () =>
    sh.submit(() =>
      act.run(pick(lang, 'Guardando…', 'Saving…'), async () => {
        await api.saveStack(id.trim(), description.trim(), picked)
        onSaved()
      }),
    )

  return (
    <div className="stack-form reg-form" role="group" aria-label={draft.isNew ? pick(lang, 'Crear un stack', 'Create a stack') : pick(lang, 'Editar el stack', 'Edit the stack')}>
      <Field
        label={pick(lang, 'Nombre', 'Name')}
        error={sh.shown('id')}
        hint={draft.isNew ? pick(lang, 'Minúsculas, números y guiones. No se puede cambiar después.', 'Lowercase letters, numbers and dashes. It cannot be changed later.') : undefined}
      >
        {(a) => <input {...a} value={id} disabled={!draft.isNew} placeholder="mi-backend" autoFocus={draft.isNew} onBlur={() => sh.touch('id')} onChange={(e) => setId(e.target.value.toLowerCase())} />}
      </Field>
      <Field
        label={pick(lang, 'Descripción (opcional)', 'Description (optional)')}
        error={sh.shown('description')}
        hint={description.length > 150 ? `${description.trim().length}/${STACK_DESCRIPTION_MAX}` : undefined}
      >
        {(a) => <input {...a} value={description} onBlur={() => sh.touch('description')} onChange={(e) => setDescription(e.target.value)} />}
      </Field>
      <fieldset className="rf-group" aria-describedby={sh.shown('providers') ? 'stack-providers-err' : undefined}>
        <legend>{pick(lang, 'Emuladores, en el orden en que los eliges', 'Emulators, in the order you pick them')}</legend>
        <div className="pick-grid">
          {providers.map((p) => (
            <label key={p.id} className={picked.includes(p.id) ? 'stack-pick on' : 'stack-pick'}>
              <input type="checkbox" checked={picked.includes(p.id)} onChange={() => toggle(p.id)} />
              {p.name}
              {picked.includes(p.id) && <small>{picked.indexOf(p.id) + 1}</small>}
            </label>
          ))}
        </div>
        {sh.shown('providers') && (
          <small id="stack-providers-err" className="err">
            {sh.shown('providers')}
          </small>
        )}
      </fieldset>
      <Feedback error={act.error} />
      <FormActions
        lang={lang}
        saving={act.busy}
        onSave={save}
        onCancel={onCancel}
        saveLabel={pick(lang, 'Guardar stack', 'Save stack')}
        invalid={sh.tried && Object.keys(errors).length > 0}
      />
    </div>
  )
}
