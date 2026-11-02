import { useCallback, useEffect, useId, useRef, useState } from 'react'
import {
  registry,
  api,
  variables,
  type ComposeDef,
  type DockerStatus,
  type ImageDef,
  type Provider,
  type SourceDef,
} from './api'
import type { Key, Lang } from './i18n'
import type { TabId } from './nav'
import {
  composeBody,
  composeSummary,
  composeTone,
  composeToDraft,
  changedFiles,
  emptyComposeDraft,
  emptyImageDraft,
  emptySourceDraft,
  fileRefLabel,
  fileStatusLabel,
  fileTone,
  formatCommand,
  imageBody,
  missingFiles,
  secretName,
  shortPath,
  sourceBody,
  sourceDetail,
  sourceStatusLabel,
  sourceTone,
  syncLabel,
  upBlocker,
  validateCompose,
  validateImage,
  validateSource,
  type ComposeDraft,
  type Errors,
  type ImageDraft,
  type SourceDraft,
  type Tone,
} from './registryLogic'
import { ago } from './systemLogic'

type T = (k: Key) => string

const pick = (lang: Lang, es: string, en: string) => (lang === 'es' ? es : en)
const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Runs a loader now and again every few seconds when asked. */
function useLoad<D>(load: () => Promise<D>, everyMs = 0): { data: D | null; error: string; reload: () => void } {
  const [data, setData] = useState<D | null>(null)
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

/** One action with its own "working" text, success note and error, so a slow step does not freeze the rest. */
function useAction() {
  const [working, setWorking] = useState('')
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const run = async (progress: string, fn: () => Promise<string | void>) => {
    setWorking(progress); setError(''); setDone('')
    try { setDone((await fn()) || '') } catch (e) { setError(message(e)) } finally { setWorking('') }
  }
  return { working, error, done, run, clear: () => { setError(''); setDone('') } }
}

function Feedback({ working, error, done }: { working: string; error: string; done: string }) {
  return (
    <>
      {working && <p className="reg-progress" role="status"><i className="spinner" aria-hidden="true" />{working}</p>}
      {error && <p className="err" role="alert">{error}</p>}
      {done && <p className="ok-note" role="status">{done}</p>}
    </>
  )
}

function Pill({ tone, children }: { tone: Tone; children: React.ReactNode }) {
  return <span className={`pill tone-${tone}`}><i className={tone === 'ok' ? 'led on' : 'dot'} />{children}</span>
}

function Empty({ title, body, action, onAction }: { title: string; body: string; action: string; onAction: () => void }) {
  return (
    <div className="reg-empty">
      <h3>{title}</h3>
      <p>{body}</p>
      <button className="primary" onClick={onAction}>{action}</button>
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Form building blocks
// ---------------------------------------------------------------------------------------------------------------------

/** A labelled input with its inline error. The error is linked with aria-describedby so screen readers read it. */
function Field({ label, error, hint, children }: { label: string; error?: string; hint?: string; children: (p: { id: string; 'aria-invalid': boolean; 'aria-describedby'?: string }) => React.ReactNode }) {
  const id = useId()
  const msgId = `${id}-msg`
  return (
    <div className="rf">
      <label htmlFor={id}>{label}</label>
      {children({ id, 'aria-invalid': !!error, 'aria-describedby': error || hint ? msgId : undefined })}
      {(error || hint) && <small id={msgId} className={error ? 'err' : 'hint'}>{error || hint}</small>}
    </div>
  )
}

/** Values as removable chips: type one and press Enter or comma. */
function ChipInput({ label, values, onChange, placeholder, error, hint, lang, inputMode }: { label: string; values: string[]; onChange: (v: string[]) => void; placeholder?: string; error?: string; hint?: string; lang: Lang; inputMode?: 'numeric' | 'text' }) {
  const [text, setText] = useState('')
  const commit = () => {
    const parts = text.split(',').map((x) => x.trim()).filter(Boolean)
    if (parts.length === 0) return
    onChange([...values, ...parts.filter((p, i) => !values.includes(p) && parts.indexOf(p) === i)])
    setText('')
  }
  return (
    <Field label={label} error={error} hint={hint}>
      {(a) => (
        <div className="chip-input">
          {values.length > 0 && (
            <ul className="chips">
              {values.map((v) => (
                <li key={v}>
                  {v}
                  <button type="button" className="chip-x" aria-label={pick(lang, `Quitar ${v}`, `Remove ${v}`)} onClick={() => onChange(values.filter((x) => x !== v))}>×</button>
                </li>
              ))}
            </ul>
          )}
          <div className="chip-add">
            <input
              {...a}
              value={text}
              inputMode={inputMode}
              placeholder={placeholder}
              onChange={(e) => setText(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); commit() }
                else if (e.key === 'Backspace' && !text && values.length > 0) onChange(values.slice(0, -1))
              }}
            />
            <button type="button" className="ghost small" onClick={commit} disabled={!text.trim()}>{pick(lang, 'Añadir', 'Add')}</button>
          </div>
        </div>
      )}
    </Field>
  )
}

/** Remembers which fields were touched so errors do not shout before people have typed anything. */
function useShown(errors: Errors) {
  const [tried, setTried] = useState(false)
  const [touched, setTouched] = useState<Set<string>>(new Set())
  const touch = (k: string) => setTouched((s) => (s.has(k) ? s : new Set(s).add(k)))
  const shown = (k: string) => (tried || touched.has(k) ? errors[k] ?? '' : '')
  const submit = (go: () => void) => {
    setTried(true)
    if (Object.keys(errors).length === 0) go()
  }
  return { shown, touch, submit, tried }
}

function FormActions({ lang, saving, onSave, onCancel, saveLabel, invalid }: { lang: Lang; saving: boolean; onSave: () => void; onCancel: () => void; saveLabel: string; invalid: boolean }) {
  return (
    <div className="actions">
      <button className="primary" disabled={saving} onClick={onSave}>{saving ? pick(lang, 'Guardando…', 'Saving…') : saveLabel}</button>
      <button className="ghost" disabled={saving} onClick={onCancel}>{pick(lang, 'Cancelar', 'Cancel')}</button>
      {invalid && <small className="err" role="status">{pick(lang, 'Revisa los campos marcados.', 'Check the highlighted fields.')}</small>}
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// The screen
// ---------------------------------------------------------------------------------------------------------------------

interface Props {
  t: T
  lang: Lang
  docker: DockerStatus | null
  providers: Provider[]
  /** Reload the provider list in the app (an own image shows up in Infrastructure as soon as it is saved). */
  onChanged: () => void
  go: (tab: TabId) => void
  /** Open the screen with one own image already being edited (the link on its card in Infrastructure). */
  focusImage?: string | null
}

export function RegistryView({ lang, docker, providers, onChanged, go, focusImage }: Props) {
  const images = useLoad(registry.images.list)
  const sources = useLoad(registry.sources.list)
  const compose = useLoad(registry.compose.list, 5000)

  const reloadImages = () => { images.reload(); onChanged() }

  return (
    <div className="registry">
      <ImagesSection lang={lang} docker={docker} providers={providers} load={images} reload={reloadImages} go={go} focus={focusImage ?? null} />
      <SourcesSection lang={lang} load={sources} reload={sources.reload} composes={compose.data ?? []} />
      <ComposeSection lang={lang} docker={docker} load={compose} sources={sources.data ?? []} reload={compose.reload} goSources={() => document.getElementById('reg-sources')?.scrollIntoView({ behavior: 'smooth' })} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// 1. Own images
// ---------------------------------------------------------------------------------------------------------------------

type Load<D> = { data: D | null; error: string; reload: () => void }

function ImagesSection({ lang, docker, providers, load, reload, go, focus }: { lang: Lang; docker: DockerStatus | null; providers: Provider[]; load: Load<ImageDef[]>; reload: () => void; go: (tab: TabId) => void; focus: string | null }) {
  const [editing, setEditing] = useState<ImageDraft | null>(null)
  const list = load.data ?? []
  const openedFocus = useRef<string | null>(null)

  useEffect(() => {
    if (!focus || openedFocus.current === focus) return
    const found = load.data?.find((i) => i.id === focus)
    if (found) { openedFocus.current = focus; setEditing(imageToDraft(found)) }
  }, [focus, load.data])

  return (
    <section className="reg-section" aria-labelledby="reg-images">
      <div className="section-head">
        <h2 className="eyebrow" id="reg-images">{pick(lang, 'Imágenes propias', 'Own images')}</h2>
        {list.length > 0 && !editing && <button className="ghost small" onClick={() => setEditing(emptyImageDraft())}>{pick(lang, '+ Registrar una imagen', '+ Register an image')}</button>}
      </div>
      {editing && <ImageForm lang={lang} draft={editing} providers={providers} images={list} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      {load.error && <p className="err" role="alert">{load.error}</p>}
      {!load.data && !load.error && <p className="muted">…</p>}
      {load.data && list.length === 0 && !editing && (
        <Empty
          title={pick(lang, 'Aún no has registrado ninguna imagen', 'You have not registered any image yet')}
          body={pick(lang, 'Aquí puedes dar de alta una imagen de Docker tuya (por ejemplo la de tu API) y Nimbo la tratará como un emulador más: la arrancas y la paras con un botón, ves sus logs, guardas sus datos y la metes en un stack junto a los demás.', 'Register a Docker image of your own (for example your API) and Nimbo treats it like one more emulator: start and stop it with a button, read its logs, keep its data and put it in a stack next to the others.')}
          action={pick(lang, 'Registrar mi primera imagen', 'Register my first image')}
          onAction={() => setEditing(emptyImageDraft())}
        />
      )}
      <div className="presets">
        {list.map((img) => (
          <ImageCard key={img.id} lang={lang} img={img} provider={providers.find((p) => p.id === img.id)} dockerUp={docker?.available === true} reload={reload} onEdit={() => setEditing(imageToDraft(img))} go={go} />
        ))}
      </div>
    </section>
  )
}

function imageToDraft(i: ImageDef): ImageDraft {
  return { id: i.id, name: i.name, image: i.image, ports: i.ports.map(String), env: i.env.map((e) => ({ ...e })), secretKeys: [...i.secretKeys], command: formatCommand(i.command), volume: i.volume ?? '', notes: i.notes, isNew: false }
}

function ImageCard({ lang, img, provider, dockerUp, reload, onEdit, go }: { lang: Lang; img: ImageDef; provider?: Provider; dockerUp: boolean; reload: () => void; onEdit: () => void; go: (tab: TabId) => void }) {
  const act = useAction()
  const state = provider?.state ?? 'not_created'
  const running = state === 'running'
  const stateLabel = { running: pick(lang, 'En marcha', 'Running'), stopped: pick(lang, 'Parado', 'Stopped'), not_created: pick(lang, 'Sin crear', 'Not created'), error: 'Error' }[state]
  const tone: Tone = running ? 'ok' : state === 'error' ? 'bad' : 'idle'

  const toggle = () =>
    act.run(running ? pick(lang, 'Parando…', 'Stopping…') : pick(lang, 'Arrancando… la primera vez puede tardar mientras se descarga la imagen.', 'Starting… the first time can take a while as the image is downloaded.'), async () => {
      try {
        if (running) await api.stop(img.id)
        else {
          if ((provider?.needsSetup?.length ?? 0) > 0) await variables.init(img.id)
          await api.start(img.id)
        }
      } finally {
        reload()
      }
    })

  const remove = () => {
    if (!window.confirm(pick(lang, `¿Quitar la imagen ${img.name} del registro? Su volumen de datos y sus secretos no se borran.`, `Remove the image ${img.name} from the registry? Its data volume and its secrets are not deleted.`))) return
    act.run(pick(lang, 'Quitando…', 'Removing…'), async () => {
      try {
        await registry.images.remove(img.id)
      } finally {
        reload()
      }
    })
  }

  return (
    <article className="preset" data-state={state}>
      <header>
        <h3>{img.name}</h3>
        <Pill tone={tone}>{stateLabel}</Pill>
      </header>
      <p className="reg-sub"><code>{img.id}</code> · <code>{img.image}</code></p>
      {img.notes && <p>{img.notes}</p>}
      <ul className="chips" aria-label={pick(lang, 'Puertos', 'Ports')}>
        {img.ports.length === 0 ? <li>{pick(lang, 'sin puertos', 'no ports')}</li> : img.ports.map((p) => <li key={p}>:{p}</li>)}
      </ul>
      <dl className="reg-facts">
        {img.env.length > 0 && (<><dt>{pick(lang, 'Variables', 'Variables')}</dt><dd>{img.env.map((e) => e.key).join(', ')}</dd></>)}
        {img.command.length > 0 && (<><dt>{pick(lang, 'Comando', 'Command')}</dt><dd><code>{formatCommand(img.command)}</code></dd></>)}
        {img.volume && (<><dt>{pick(lang, 'Datos en', 'Data at')}</dt><dd><code>{img.volume}</code></dd></>)}
        {img.secrets.length > 0 && (
          <>
            <dt>{pick(lang, 'Secretos', 'Secrets')}</dt>
            <dd>
              {img.secrets.map((s) => (
                <span key={s.key} className="reg-secret">
                  <code>{s.key}</code> <Pill tone={s.set ? 'ok' : 'warn'}>{s.set ? pick(lang, 'con valor', 'set') : pick(lang, 'sin valor', 'not set')}</Pill>
                </span>
              ))}
              <button className="link" onClick={() => go('settings')}>{pick(lang, 'Darles valor en Ajustes', 'Set their values in Settings')}</button>
            </dd>
          </>
        )}
      </dl>
      <Feedback working={act.working} error={act.error} done={act.done} />
      <div className="actions">
        <button className={running ? undefined : 'primary'} disabled={!!act.working || (!running && !dockerUp)} onClick={toggle}>{running ? pick(lang, 'Parar', 'Stop') : pick(lang, 'Arrancar', 'Start')}</button>
        <button className="ghost" onClick={() => go('infra')}>{pick(lang, 'Logs y entorno', 'Logs and environment')}</button>
        <button className="ghost" disabled={!!act.working} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <button className="ghost danger" disabled={!!act.working} onClick={remove}>{pick(lang, 'Quitar', 'Remove')}</button>
      </div>
      {state !== 'not_created' && !running && <small className="hint">{pick(lang, 'Para quitarla del registro, elimina antes su contenedor en Infraestructura.', 'To remove it from the registry, first remove its container in Infrastructure.')}</small>}
    </article>
  )
}

function ImageForm({ lang, draft, providers, images, onCancel, onSaved }: { lang: Lang; draft: ImageDraft; providers: Provider[]; images: ImageDef[]; onCancel: () => void; onSaved: () => void }) {
  const [d, setD] = useState(draft)
  const set = (patch: Partial<ImageDraft>) => setD((cur) => ({ ...cur, ...patch }))
  const act = useAction()

  const takenIds = [...providers.filter((p) => !p.custom).map((p) => p.id), ...images.filter((i) => i.id !== draft.id).map((i) => i.id)]
  const usedPorts: Record<string, string> = {}
  for (const p of providers) if (p.id !== draft.id) for (const port of p.ports) usedPorts[String(port)] = p.name
  const errors = validateImage(d, { takenIds, usedPorts }, lang)
  const sh = useShown(errors)

  const save = () =>
    sh.submit(() =>
      act.run(pick(lang, 'Guardando…', 'Saving…'), async () => {
        await registry.images.save(d.id, imageBody(d))
        onSaved()
      }),
    )

  const setEnv = (i: number, patch: Partial<{ key: string; value: string }>) => set({ env: d.env.map((r, j) => (j === i ? { ...r, ...patch } : r)) })

  return (
    <div className="stack-form reg-form" role="group" aria-label={d.isNew ? pick(lang, 'Registrar una imagen', 'Register an image') : pick(lang, 'Editar la imagen', 'Edit the image')}>
      <p className="muted">{pick(lang, 'Nimbo arranca esta imagen en tu equipo, con sus puertos solo en 127.0.0.1. No se admiten opciones avanzadas de Docker (modo privilegiado, montar carpetas del equipo, redes) a propósito.', 'Nimbo runs this image on your computer with its ports on 127.0.0.1 only. Advanced Docker options (privileged mode, host folder mounts, networks) are not supported, on purpose.')}</p>
      <div className="rf-grid">
        <Field label={pick(lang, 'Identificador', 'Identifier')} error={sh.shown('id')} hint={d.isNew ? pick(lang, 'Se usa como nombre del contenedor: nimbo-<identificador>.', 'Used as the container name: nimbo-<identifier>.') : undefined}>
          {(a) => <input {...a} value={d.id} disabled={!d.isNew} placeholder="my-api" onBlur={() => sh.touch('id')} onChange={(e) => set({ id: e.target.value.toLowerCase() })} />}
        </Field>
        <Field label={pick(lang, 'Nombre', 'Name')} error={sh.shown('name')}>
          {(a) => <input {...a} value={d.name} placeholder="My API" onBlur={() => sh.touch('name')} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
      </div>
      <Field label={pick(lang, 'Imagen de Docker', 'Docker image')} error={sh.shown('image')} hint={pick(lang, 'Con su etiqueta, por ejemplo ghcr.io/yo/api:1.2.', 'With its tag, for example ghcr.io/me/api:1.2.')}>
        {(a) => <input {...a} value={d.image} placeholder="ghcr.io/me/api:1.2" onBlur={() => sh.touch('image')} onChange={(e) => set({ image: e.target.value.trim() })} />}
      </Field>
      <ChipInput lang={lang} label={pick(lang, 'Puertos', 'Ports')} values={d.ports} onChange={(v) => { set({ ports: v }); sh.touch('ports') }} placeholder="8081" inputMode="numeric" error={sh.shown('ports')} hint={pick(lang, 'Entre 1024 y 65535. El mismo número se usa en tu equipo y dentro del contenedor.', 'Between 1024 and 65535. The same number is used on your computer and inside the container.')} />

      <fieldset className="rf-group">
        <legend>{pick(lang, 'Variables de entorno', 'Environment variables')}</legend>
        {d.env.map((r, i) => (
          <div className="rf-row" key={i}>
            <input aria-label={pick(lang, 'Nombre de la variable', 'Variable name')} value={r.key} placeholder="MODE" onChange={(e) => { setEnv(i, { key: e.target.value }); sh.touch('env') }} />
            <input aria-label={pick(lang, 'Valor', 'Value')} value={r.value} placeholder="test" onChange={(e) => { setEnv(i, { value: e.target.value }); sh.touch('env') }} />
            <button type="button" className="ghost small" aria-label={pick(lang, 'Quitar variable', 'Remove variable')} onClick={() => set({ env: d.env.filter((_, j) => j !== i) })}>×</button>
          </div>
        ))}
        <div><button type="button" className="ghost small" onClick={() => set({ env: [...d.env, { key: '', value: '' }] })}>{pick(lang, '+ Añadir variable', '+ Add a variable')}</button></div>
        {sh.shown('env') && <small className="err" role="alert">{sh.shown('env')}</small>}
        <small className="hint">{pick(lang, 'Valores que no son secretos. Para contraseñas y claves usa los secretos de abajo.', 'Values that are not secret. For passwords and keys use the secrets below.')}</small>
      </fieldset>

      <ChipInput lang={lang} label={pick(lang, 'Secretos (solo los nombres)', 'Secrets (names only)')} values={d.secretKeys} onChange={(v) => { set({ secretKeys: v }); sh.touch('secretKeys') }} placeholder="API_KEY" error={sh.shown('secretKeys')} hint={pick(lang, 'Los valores se escriben en Ajustes > Variables de emuladores, nunca aquí.', 'The values are typed under Settings > Emulator variables, never here.')} />
      {d.secretKeys.length > 0 && d.id && (
        <p className="hint">{pick(lang, 'En Ajustes aparecerán como: ', 'In Settings they will appear as: ')}{d.secretKeys.map((k) => <code key={k}>{secretName(d.id, k)}</code>).reduce<React.ReactNode[]>((acc, x, i) => (i ? [...acc, ' ', x] : [x]), [])}</p>
      )}

      <div className="rf-grid">
        <Field label={pick(lang, 'Comando (opcional)', 'Command (optional)')} error={sh.shown('command')} hint={pick(lang, 'Sustituye al de la imagen. Las comillas agrupan palabras.', 'Replaces the image default. Quotes group words.')}>
          {(a) => <input {...a} value={d.command} placeholder="serve --dev" onBlur={() => sh.touch('command')} onChange={(e) => set({ command: e.target.value })} />}
        </Field>
        <Field label={pick(lang, 'Carpeta de datos (opcional)', 'Data folder (optional)')} error={sh.shown('volume')} hint={pick(lang, 'Ruta dentro del contenedor que se conserva con "Conservar datos".', 'Path inside the container kept by "Keep data".')}>
          {(a) => <input {...a} value={d.volume} placeholder="/data" onBlur={() => sh.touch('volume')} onChange={(e) => set({ volume: e.target.value })} />}
        </Field>
      </div>
      <Field label={pick(lang, 'Notas (opcional)', 'Notes (optional)')} error={sh.shown('notes')}>
        {(a) => <textarea {...a} rows={2} value={d.notes} onBlur={() => sh.touch('notes')} onChange={(e) => set({ notes: e.target.value })} />}
      </Field>

      <Feedback working="" error={act.error} done="" />
      <FormActions lang={lang} saving={!!act.working} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Registrar imagen', 'Register image') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// 2. Sources
// ---------------------------------------------------------------------------------------------------------------------

function SourcesSection({ lang, load, reload, composes }: { lang: Lang; load: Load<SourceDef[]>; reload: () => void; composes: ComposeDef[] }) {
  const [editing, setEditing] = useState<SourceDraft | null>(null)
  const list = load.data ?? []
  const usedBy = (id: string) => composes.filter((c) => c.files.some((f) => f.source === id) || c.envFile?.source === id).map((c) => c.name || c.id)

  return (
    <section className="reg-section" id="reg-sources" aria-labelledby="reg-sources-h">
      <div className="section-head">
        <h2 className="eyebrow" id="reg-sources-h">{pick(lang, 'Repositorios', 'Repositories')}</h2>
        {list.length > 0 && !editing && <button className="ghost small" onClick={() => setEditing(emptySourceDraft())}>{pick(lang, '+ Enlazar un repositorio', '+ Link a repository')}</button>}
      </div>
      {editing && <SourceForm lang={lang} draft={editing} ids={list.map((s) => s.id)} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      {load.error && <p className="err" role="alert">{load.error}</p>}
      {!load.data && !load.error && <p className="muted">…</p>}
      {load.data && list.length === 0 && !editing && (
        <Empty
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

  const remove = () => {
    const extra = src.kind === 'git' ? pick(lang, ' La copia clonada en la carpeta de datos de Nimbo se borrará.', ' The clone in Nimbo\'s data folder will be deleted.') : pick(lang, ' Tu carpeta no se toca.', ' Your folder is not touched.')
    if (!window.confirm(pick(lang, `¿Quitar el repositorio ${src.id}?${extra}`, `Remove the repository ${src.id}?${extra}`))) return
    act.run(pick(lang, 'Quitando…', 'Removing…'), async () => {
      try {
        await registry.sources.remove(src.id)
      } finally {
        reload()
      }
    })
  }

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
        <button className="primary" disabled={!!act.working} onClick={sync}>{syncLabel(src, lang)}</button>
        <button className="ghost" disabled={!!act.working} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <button className="ghost danger" disabled={!!act.working} onClick={remove}>{pick(lang, 'Quitar', 'Remove')}</button>
      </div>
    </article>
  )
}

function SourceForm({ lang, draft, ids, onCancel, onSaved }: { lang: Lang; draft: SourceDraft; ids: string[]; onCancel: () => void; onSaved: () => void }) {
  const [d, setD] = useState(draft)
  const set = (patch: Partial<SourceDraft>) => setD((cur) => ({ ...cur, ...patch }))
  const act = useAction()
  const errors = validateSource(d, ids, lang)
  const sh = useShown(errors)

  const save = () =>
    sh.submit(() =>
      act.run(pick(lang, 'Guardando…', 'Saving…'), async () => {
        await registry.sources.save(d.id, sourceBody(d))
        onSaved()
      }),
    )

  return (
    <div className="stack-form reg-form" role="group" aria-label={d.isNew ? pick(lang, 'Enlazar un repositorio', 'Link a repository') : pick(lang, 'Editar el repositorio', 'Edit the repository')}>
      <Field label={pick(lang, 'Identificador', 'Identifier')} error={sh.shown('id')} hint={pick(lang, 'Los perfiles de compose lo usan para encontrar sus archivos.', 'Compose profiles use it to find their files.')}>
        {(a) => <input {...a} value={d.id} disabled={!d.isNew} placeholder="my-repo" onBlur={() => sh.touch('id')} onChange={(e) => set({ id: e.target.value.toLowerCase() })} />}
      </Field>
      <fieldset className="rf-group">
        <legend>{pick(lang, 'Dónde está', 'Where it lives')}</legend>
        <div className="seg" role="radiogroup" aria-label={pick(lang, 'Tipo de repositorio', 'Repository type')}>
          <label className={d.kind === 'local' ? 'pick on' : 'pick'}>
            <input type="radio" name="src-kind" checked={d.kind === 'local'} disabled={!d.isNew} onChange={() => set({ kind: 'local' })} />
            {pick(lang, 'Carpeta de mi equipo', 'Folder on my computer')}
          </label>
          <label className={d.kind === 'git' ? 'pick on' : 'pick'}>
            <input type="radio" name="src-kind" checked={d.kind === 'git'} disabled={!d.isNew} onChange={() => set({ kind: 'git' })} />
            {pick(lang, 'Dirección de Git', 'Git address')}
          </label>
        </div>
      </fieldset>
      {d.kind === 'local' ? (
        <Field label={pick(lang, 'Carpeta', 'Folder')} error={sh.shown('path')} hint={pick(lang, 'Un repositorio que ya tienes clonado. Nimbo lo lee en vivo, nunca lo copia.', 'A repository you already cloned. Nimbo reads it live and never copies it.')}>
          {(a) => <input {...a} value={d.path} placeholder="C:\code\my-repo" onBlur={() => sh.touch('path')} onChange={(e) => set({ path: e.target.value })} />}
        </Field>
      ) : (
        <div className="rf-grid">
          <Field label={pick(lang, 'Dirección', 'Address')} error={sh.shown('url')} hint={pick(lang, 'Nimbo usa tu propia configuración de Git para entrar; no guarda contraseñas.', 'Nimbo uses your own Git setup to sign in; it stores no passwords.')}>
            {(a) => <input {...a} value={d.url} placeholder="https://github.com/me/repo.git" onBlur={() => sh.touch('url')} onChange={(e) => set({ url: e.target.value.trim() })} />}
          </Field>
          <Field label={pick(lang, 'Rama (opcional)', 'Branch (optional)')} error={sh.shown('branch')}>
            {(a) => <input {...a} value={d.branch} placeholder="main" onBlur={() => sh.touch('branch')} onChange={(e) => set({ branch: e.target.value.trim() })} />}
          </Field>
        </div>
      )}
      <Feedback working="" error={act.error} done="" />
      <FormActions lang={lang} saving={!!act.working} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Enlazar', 'Link') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// 3. Compose profiles
// ---------------------------------------------------------------------------------------------------------------------

function ComposeSection({ lang, docker, load, sources, reload, goSources }: { lang: Lang; docker: DockerStatus | null; load: Load<ComposeDef[]>; sources: SourceDef[]; reload: () => void; goSources: () => void }) {
  const [editing, setEditing] = useState<ComposeDraft | null>(null)
  const list = load.data ?? []

  return (
    <section className="reg-section" aria-labelledby="reg-compose">
      <div className="section-head">
        <h2 className="eyebrow" id="reg-compose">{pick(lang, 'Perfiles de compose', 'Compose profiles')}</h2>
        {list.length > 0 && !editing && <button className="ghost small" onClick={() => setEditing(emptyComposeDraft())}>{pick(lang, '+ Crear un perfil', '+ Create a profile')}</button>}
      </div>
      {editing && <ComposeForm lang={lang} draft={editing} sources={sources} ids={list.map((c) => c.id)} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} goSources={goSources} />}
      {load.error && <p className="err" role="alert">{load.error}</p>}
      {!load.data && !load.error && <p className="muted">…</p>}
      {load.data && list.length === 0 && !editing && (
        <Empty
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

type ComposePanel = null | 'file' | 'logs' | 'down'

function ComposeCard({ lang, def, dockerUp, reload, onEdit }: { lang: Lang; def: ComposeDef; dockerUp: boolean; reload: () => void; onEdit: () => void }) {
  const act = useAction()
  const [panel, setPanel] = useState<ComposePanel>(null)
  const [volumes, setVolumes] = useState(false)
  const busy = !!act.working
  const blocker = upBlocker(def, dockerUp, lang)
  const changed = changedFiles(def)
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
      try {
        await registry.compose.down(def.id, volumes)
      } finally {
        reload()
        setPanel(null)
      }
      return volumes ? pick(lang, 'Entorno abajo y volúmenes borrados.', 'Environment down and volumes deleted.') : pick(lang, 'Entorno abajo.', 'Environment is down.')
    })

  const remove = () => {
    if (!window.confirm(pick(lang, `¿Borrar el perfil ${def.name}? Tus archivos de compose no se tocan.`, `Delete the profile ${def.name}? Your compose files are not touched.`))) return
    act.run(pick(lang, 'Borrando…', 'Deleting…'), async () => {
      try {
        await registry.compose.remove(def.id)
      } finally {
        reload()
      }
    })
  }

  const statuses = [...def.fileStatus]
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
      {changed.length > 0 && (
        <p className="reg-note" role="status">
          {running
            ? pick(lang, `${changed.length === 1 ? 'Un archivo cambió' : `${changed.length} archivos cambiaron`} desde el último "Subir". Vuelve a subir el perfil para aplicar los cambios.`, `${changed.length === 1 ? 'A file changed' : `${changed.length} files changed`} since the last "Up". Bring the profile up again to apply the changes.`)
            : pick(lang, `${changed.length === 1 ? 'Un archivo cambió' : `${changed.length} archivos cambiaron`} desde el último "Subir".`, `${changed.length === 1 ? 'A file changed' : `${changed.length} files changed`} since the last "Up".`)}
        </p>
      )}
      {missingFiles(def).length > 0 && <p className="err">{pick(lang, 'No se encuentra: ', 'Not found: ')}{missingFiles(def).map(shortPath).join(', ')}</p>}
      {def.lastUpAt && <small className="hint">{pick(lang, 'Última subida', 'Last up')} {ago(def.lastUpAt, Date.now(), lang)}</small>}

      <Feedback working={act.working} error={act.error} done={act.done} />

      <div className="actions">
        <button className="primary" disabled={busy || !!blocker} title={blocker || undefined} onClick={up}>{pick(lang, 'Subir', 'Up')}</button>
        <button className="ghost" disabled={busy || !running} onClick={() => { act.clear(); toggle('down') }} aria-expanded={panel === 'down'}>{pick(lang, 'Bajar', 'Down')}</button>
        <button className="ghost" disabled={busy || !dockerUp} onClick={validate}>{pick(lang, 'Validar', 'Validate')}</button>
        <button className="ghost" onClick={() => toggle('file')} aria-expanded={panel === 'file'}>{pick(lang, 'Ver archivo', 'View file')}</button>
        <button className="ghost" onClick={() => toggle('logs')} aria-expanded={panel === 'logs'}>Logs</button>
        <button className="ghost" disabled={busy} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <button className="ghost danger" disabled={busy || running} title={running ? pick(lang, 'Bájalo antes', 'Take it down first') : undefined} onClick={remove}>{pick(lang, 'Borrar', 'Delete')}</button>
      </div>
      {blocker && <small className="hint">{blocker}</small>}

      {panel === 'down' && (
        <div className="reg-panel" role="group" aria-label={pick(lang, 'Confirmar bajar', 'Confirm taking down')}>
          <p>{pick(lang, 'Se pararán y eliminarán los contenedores de este perfil. Tus archivos no se tocan.', 'The containers of this profile will be stopped and removed. Your files are not touched.')}</p>
          <label className="check"><input type="checkbox" checked={volumes} onChange={(e) => setVolumes(e.target.checked)} /> {pick(lang, 'Borrar también los volúmenes (se pierden sus datos)', 'Also delete the volumes (their data is lost)')}</label>
          <div className="actions">
            <button className="danger" disabled={busy} onClick={down}>{pick(lang, 'Sí, bajar', 'Yes, take down')}</button>
            <button className="ghost" onClick={() => setPanel(null)}>{pick(lang, 'Cancelar', 'Cancel')}</button>
          </div>
        </div>
      )}
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
          <small className="hint">{pick(lang, 'Leído ahora mismo de ', 'Read just now from ')}<code>{file.data.path}</code></small>
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
      </div>
      {logs.error && <p className="err" role="alert">{logs.error}</p>}
      {!logs.data && !logs.error && <p className="muted">…</p>}
      {logs.data && <pre className="reg-pre" tabIndex={0}>{logs.data.lines.trim() || pick(lang, 'Sin líneas de log todavía.', 'No log lines yet.')}</pre>}
    </div>
  )
}

function ComposeForm({ lang, draft, sources, ids, onCancel, onSaved, goSources }: { lang: Lang; draft: ComposeDraft; sources: SourceDef[]; ids: string[]; onCancel: () => void; onSaved: () => void; goSources: () => void }) {
  const [d, setD] = useState(draft)
  const set = (patch: Partial<ComposeDraft>) => setD((cur) => ({ ...cur, ...patch }))
  const act = useAction()
  const errors = validateCompose(d, ids, sources.map((s) => s.id), lang)
  const sh = useShown(errors)

  const setFile = (i: number, patch: Partial<{ source: string; path: string }>) => set({ files: d.files.map((f, j) => (j === i ? { ...f, ...patch } : f)) })
  const sourceSelect = (value: string, onChange: (v: string) => void, label: string) => (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{pick(lang, 'Ruta completa', 'Full path')}</option>
      {sources.map((s) => <option key={s.id} value={s.id}>{s.id}</option>)}
    </select>
  )

  const save = () =>
    sh.submit(() =>
      act.run(pick(lang, 'Guardando…', 'Saving…'), async () => {
        await registry.compose.save(d.id, composeBody(d))
        onSaved()
      }),
    )

  return (
    <div className="stack-form reg-form" role="group" aria-label={d.isNew ? pick(lang, 'Crear un perfil', 'Create a profile') : pick(lang, 'Editar el perfil', 'Edit the profile')}>
      <div className="rf-grid">
        <Field label={pick(lang, 'Identificador', 'Identifier')} error={sh.shown('id')} hint={d.isNew ? pick(lang, 'El proyecto de compose se llamará nimbo-reg-<identificador>.', 'The compose project will be called nimbo-reg-<identifier>.') : undefined}>
          {(a) => <input {...a} value={d.id} disabled={!d.isNew} placeholder="api-tests" onBlur={() => sh.touch('id')} onChange={(e) => set({ id: e.target.value.toLowerCase() })} />}
        </Field>
        <Field label={pick(lang, 'Nombre', 'Name')} error={sh.shown('name')}>
          {(a) => <input {...a} value={d.name} placeholder="API tests" onBlur={() => sh.touch('name')} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
      </div>
      <Field label={pick(lang, 'Para qué sirve (opcional)', 'What it is for (optional)')} error={sh.shown('purpose')}>
        {(a) => <input {...a} value={d.purpose} placeholder={pick(lang, 'pruebas de integración de la API', 'integration tests of the API')} onBlur={() => sh.touch('purpose')} onChange={(e) => set({ purpose: e.target.value })} />}
      </Field>

      <fieldset className="rf-group">
        <legend>{pick(lang, 'Archivos de compose, en orden', 'Compose files, in order')}</legend>
        {sources.length === 0 && (
          <small className="hint">{pick(lang, 'Sin repositorios solo puedes usar rutas completas. ', 'Without repositories you can only use full paths. ')}<button type="button" className="link" onClick={goSources}>{pick(lang, 'Enlazar un repositorio', 'Link a repository')}</button></small>
        )}
        {d.files.map((f, i) => (
          <div key={i}>
            <div className="rf-row rf-file">
              {sourceSelect(f.source, (v) => setFile(i, { source: v }), pick(lang, 'Repositorio del archivo', 'File repository'))}
              <input aria-label={pick(lang, 'Ruta del archivo', 'File path')} value={f.path} placeholder={f.source ? 'deploy/compose.test.yml' : 'C:\\code\\deploy\\compose.yml'} onChange={(e) => { setFile(i, { path: e.target.value }); sh.touch(`file${i}`) }} onBlur={() => sh.touch(`file${i}`)} />
              <button type="button" className="ghost small" disabled={d.files.length === 1} aria-label={pick(lang, 'Quitar archivo', 'Remove file')} onClick={() => set({ files: d.files.filter((_, j) => j !== i) })}>×</button>
            </div>
            {sh.shown(`file${i}`) && <small className="err" role="alert">{sh.shown(`file${i}`)}</small>}
          </div>
        ))}
        <div><button type="button" className="ghost small" onClick={() => set({ files: [...d.files, { source: d.files[d.files.length - 1]?.source ?? '', path: '' }] })}>{pick(lang, '+ Añadir otro archivo', '+ Add another file')}</button></div>
        {sh.shown('files') && <small className="err">{sh.shown('files')}</small>}
        <small className="hint">{pick(lang, 'Con un repositorio la ruta es relativa a su carpeta. Los archivos se leen de nuevo cada vez.', 'With a repository the path is relative to its folder. Files are read again every time.')}</small>
      </fieldset>

      <ChipInput lang={lang} label={pick(lang, 'Perfiles de compose (opcional)', 'Compose profiles (optional)')} values={d.profiles} onChange={(v) => { set({ profiles: v }); sh.touch('profiles') }} placeholder="tests" error={sh.shown('profiles')} hint={pick(lang, 'Cada uno se pasa como --profile. Sin ninguno, solo arrancan los servicios sin perfil.', 'Each is passed as --profile. With none, only services without a profile start.')} />

      <fieldset className="rf-group">
        <legend>{pick(lang, 'Archivo .env (opcional)', '.env file (optional)')}</legend>
        <div className="rf-row rf-file">
          {sourceSelect(d.envFile.source, (v) => set({ envFile: { ...d.envFile, source: v } }), pick(lang, 'Repositorio del .env', '.env repository'))}
          <input aria-label={pick(lang, 'Ruta del .env', '.env path')} value={d.envFile.path} placeholder={d.envFile.source ? '.env.test' : 'C:\\code\\deploy\\.env'} onChange={(e) => { set({ envFile: { ...d.envFile, path: e.target.value } }); sh.touch('env') }} onBlur={() => sh.touch('env')} />
          <button type="button" className="ghost small" disabled={!d.envFile.path} aria-label={pick(lang, 'Quitar .env', 'Remove .env')} onClick={() => set({ envFile: { source: '', path: '' } })}>×</button>
        </div>
        {sh.shown('env') && <small className="err" role="alert">{sh.shown('env')}</small>}
        <small className="hint">{pick(lang, 'Nimbo nunca lee los valores del .env: solo se lo pasa a docker compose.', 'Nimbo never reads the values in the .env: it only hands the file to docker compose.')}</small>
      </fieldset>

      <Feedback working="" error={act.error} done="" />
      <FormActions lang={lang} saving={!!act.working} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Crear perfil', 'Create profile') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}
