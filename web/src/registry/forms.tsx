import { useState } from 'react'
import { registry, type ImageDef, type Provider, type SourceDef } from '../api'
import type { Lang } from '../i18n'
import {
  composeBody,
  imageBody,
  secretName,
  sourceBody,
  validateCompose,
  validateImage,
  validateSource,
  type ComposeDraft,
  type ImageDraft,
  type SourceDraft,
} from '../registryLogic'
import { ChipInput, Feedback, Field, FormActions, pick, useAction, useShown } from '../ui'
import { spaced } from './parts'

// ---------------------------------------------------------------------------------------------------------------------
// Own image
// ---------------------------------------------------------------------------------------------------------------------

export function ImageForm({ lang, draft, providers, images, onCancel, onSaved }: { lang: Lang; draft: ImageDraft; providers: Provider[]; images: ImageDef[]; onCancel: () => void; onSaved: () => void }) {
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
        <p className="hint">{pick(lang, 'En Ajustes aparecerán como: ', 'In Settings they will appear as: ')}{spaced(d.secretKeys.map((k) => <code key={k}>{secretName(d.id, k)}</code>))}</p>
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

      <Feedback error={act.error} />
      <FormActions lang={lang} saving={act.busy} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Registrar imagen', 'Register image') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------------------------------------------------

export function SourceForm({ lang, draft, ids, onCancel, onSaved }: { lang: Lang; draft: SourceDraft; ids: string[]; onCancel: () => void; onSaved: () => void }) {
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
      <Feedback error={act.error} />
      <FormActions lang={lang} saving={act.busy} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Enlazar', 'Link') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Compose profile
// ---------------------------------------------------------------------------------------------------------------------

export function ComposeForm({ lang, draft, sources, ids, onCancel, onSaved, goSources }: { lang: Lang; draft: ComposeDraft; sources: SourceDef[]; ids: string[]; onCancel: () => void; onSaved: () => void; goSources: () => void }) {
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

      <Feedback error={act.error} />
      <FormActions lang={lang} saving={act.busy} onSave={save} onCancel={onCancel} saveLabel={d.isNew ? pick(lang, 'Crear perfil', 'Create profile') : pick(lang, 'Guardar cambios', 'Save changes')} invalid={sh.tried && Object.keys(errors).length > 0} />
    </div>
  )
}
