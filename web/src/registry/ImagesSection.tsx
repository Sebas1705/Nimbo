import { useEffect, useRef, useState, type ReactNode } from 'react'
import { api, registry, variables, type DockerStatus, type ImageDef, type Provider } from '../api'
import type { Lang } from '../i18n'
import type { TabId } from '../nav'
import { emptyImageDraft, formatCommand, type ImageDraft } from '../registryLogic'
import { ConfirmButton, EmptyState, Facts, Feedback, Pill, pick, useAction, type Tone } from '../ui'
import { ImageForm } from './forms'
import { LoadState, SectionHead, type Load } from './parts'

function imageToDraft(i: ImageDef): ImageDraft {
  return { id: i.id, name: i.name, image: i.image, ports: i.ports.map(String), env: i.env.map((e) => ({ ...e })), secretKeys: [...i.secretKeys], command: formatCommand(i.command), volume: i.volume ?? '', notes: i.notes, isNew: false }
}

export function ImagesSection({ lang, docker, providers, load, reload, go, focus }: { lang: Lang; docker: DockerStatus | null; providers: Provider[]; load: Load<ImageDef[]>; reload: () => void; go: (tab: TabId) => void; focus: string | null }) {
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
      <SectionHead id="reg-images" title={pick(lang, 'Imágenes propias', 'Own images')} addLabel={list.length > 0 && !editing ? pick(lang, '+ Registrar una imagen', '+ Register an image') : undefined} onAdd={() => setEditing(emptyImageDraft())} />
      {editing && <ImageForm lang={lang} draft={editing} providers={providers} images={list} onCancel={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      <LoadState load={load} />
      {load.data && list.length === 0 && !editing && (
        <EmptyState
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

  const remove = () =>
    act.run(pick(lang, 'Quitando…', 'Removing…'), async () => {
      try {
        await registry.images.remove(img.id)
      } finally {
        reload()
      }
    })

  const facts: Array<[string, ReactNode]> = []
  if (img.env.length > 0) facts.push([pick(lang, 'Variables', 'Variables'), img.env.map((e) => e.key).join(', ')])
  if (img.command.length > 0) facts.push([pick(lang, 'Comando', 'Command'), <code key="c">{formatCommand(img.command)}</code>])
  if (img.volume) facts.push([pick(lang, 'Datos en', 'Data at'), <code key="v">{img.volume}</code>])
  if (img.secrets.length > 0) {
    facts.push([
      pick(lang, 'Secretos', 'Secrets'),
      <>
        {img.secrets.map((s) => (
          <span key={s.key} className="reg-secret">
            <code>{s.key}</code> <Pill tone={s.set ? 'ok' : 'warn'}>{s.set ? pick(lang, 'con valor', 'set') : pick(lang, 'sin valor', 'not set')}</Pill>
          </span>
        ))}
        <button className="link" onClick={() => go('settings')}>{pick(lang, 'Darles valor en Ajustes', 'Set their values in Settings')}</button>
      </>,
    ])
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
      {facts.length > 0 && <Facts items={facts} />}
      <Feedback working={act.working} error={act.error} done={act.done} />
      <div className="actions">
        <button className={running ? undefined : 'primary'} disabled={act.busy || (!running && !dockerUp)} onClick={toggle}>{running ? pick(lang, 'Parar', 'Stop') : pick(lang, 'Arrancar', 'Start')}</button>
        <button className="ghost" onClick={() => go('infra')}>{pick(lang, 'Logs y entorno', 'Logs and environment')}</button>
        <button className="ghost" disabled={act.busy} onClick={onEdit}>{pick(lang, 'Editar', 'Edit')}</button>
        <ConfirmButton
          lang={lang}
          label={pick(lang, 'Quitar', 'Remove')}
          question={pick(lang, `¿Quitar la imagen ${img.name} del registro? Su volumen de datos y sus secretos no se borran.`, `Remove the image ${img.name} from the registry? Its data volume and its secrets are not deleted.`)}
          confirmLabel={pick(lang, 'Sí, quitar', 'Yes, remove')}
          disabled={act.busy}
          onConfirm={remove}
        />
      </div>
      {state !== 'not_created' && !running && <small className="hint">{pick(lang, 'Para quitarla del registro, elimina antes su contenedor en Infraestructura.', 'To remove it from the registry, first remove its container in Infrastructure.')}</small>}
    </article>
  )
}
