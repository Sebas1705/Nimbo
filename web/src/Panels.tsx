import { useEffect, useState } from 'react'
import { api, type ExportFormat, type Provider, type Stack } from './api'
import type { Key, Lang } from './i18n'

type T = (k: Key) => string

export type Panel = { kind: 'logs' | 'env'; provider: Provider } | null

async function copyText(text: string, done: () => void) {
  try {
    await navigator.clipboard.writeText(text)
    done()
  } catch {
    /* clipboard blocked: the text is still selectable */
  }
}

/** Logs or client variables of one provider, in a drawer. Logs refresh while it is open. */
export function SidePanel({ panel, t, onClose }: { panel: NonNullable<Panel>; t: T; onClose: () => void }) {
  const [text, setText] = useState('')
  const [copied, setCopied] = useState(false)
  const [withSecrets, setWithSecrets] = useState(false)

  useEffect(() => {
    let alive = true
    const load = () =>
      (panel.kind === 'logs' ? api.logs(panel.provider.id).then((r) => r.lines) : api.env(panel.provider.id, withSecrets).then((r) => r.dotenv))
        .then((v) => alive && setText(v))
        .catch((e) => alive && setText(e instanceof Error ? e.message : String(e)))
    load()
    const id = panel.kind === 'logs' ? setInterval(load, 2500) : undefined
    return () => {
      alive = false
      if (id) clearInterval(id)
    }
  }, [panel, withSecrets])

  return (
    <aside className="panel" role="dialog" aria-label={panel.provider.name}>
      <div className="panel-head">
        <h3>
          {panel.provider.name} · {t(panel.kind)}
        </h3>
        <div>
          <button
            className="ghost"
            onClick={() =>
              copyText(text, () => {
                setCopied(true)
                setTimeout(() => setCopied(false), 1500)
              })
            }
          >
            {copied ? t('copied') : t('copy')}
          </button>
          {panel.kind === 'env' && (
            <button className="ghost" onClick={() => setWithSecrets((v) => !v)} title="Los valores reales quedan anotados en el diario / Real values are noted in the journal">
              {withSecrets ? 'Ocultar contraseñas / Hide passwords' : 'Mostrar contraseñas / Show passwords'}
            </button>
          )}
          <button className="ghost" onClick={onClose}>
            {t('close')}
          </button>
        </div>
      </div>
      <pre>{text || t('noLogs')}</pre>
    </aside>
  )
}

interface FormatInfo {
  id: ExportFormat
  title: { es: string; en: string }
  /** What it is and where it goes, in a sentence. */
  help: { es: string; en: string }
  /** Which options make sense for it. */
  chaos?: boolean
  keepData?: boolean
  passwords?: boolean
  /** Highlights the syntax of the file in the preview. */
  needsAws?: boolean
}

interface FormatGroup {
  title: { es: string; en: string }
  intro: { es: string; en: string }
  formats: FormatInfo[]
}

/** The files Nimbo writes, grouped by what you want to do: run the stack yourself, connect an application, or drive a tool. */
export const EXPORT_GROUPS: FormatGroup[] = [
  {
    title: { es: 'Levantarlo sin Nimbo', en: 'Run it without Nimbo' },
    intro: { es: 'Un docker-compose.yml y su .env: los mismos emuladores, con sus puertos en loopback y sin ninguna contraseña dentro.', en: 'A docker-compose.yml and its .env: the same emulators, with their ports on loopback and no password inside.' },
    formats: [
      { id: 'compose', title: { es: 'docker-compose.yml', en: 'docker-compose.yml' }, help: { es: 'Guárdalo como docker-compose.yml y ejecuta docker compose up -d. Las contraseñas son referencias ${NIMBO_…} que salen del .env.', en: 'Save it as docker-compose.yml and run docker compose up -d. Passwords are ${NIMBO_…} references that come from the .env.' }, keepData: true },
      { id: 'composeenv', title: { es: '.env del compose', en: 'compose .env' }, help: { es: 'Guárdalo como .env junto al compose y no lo subas a git. Sin contraseñas reales salen vacías; con ellas, rellenas.', en: 'Save it as .env next to the compose file and keep it out of git. Without real passwords they are empty; with them, filled in.' }, passwords: true },
    ],
  },
  {
    title: { es: 'Conectar tu aplicación', en: 'Connect your application' },
    intro: { es: 'Archivos de configuración ya rellenos con las direcciones, usuarios y claves de estos emuladores.', en: 'Configuration files already filled in with the addresses, users and keys of these emulators.' },
    formats: [
      { id: 'env', title: { es: 'Variables (.env)', en: 'Variables (.env)' }, help: { es: 'Variables de entorno para cualquier lenguaje: Node, Python, Go, Java… Cárgalas con dotenv, direnv o tu IDE.', en: 'Environment variables for any language: Node, Python, Go, Java… Load them with dotenv, direnv or your IDE.' }, chaos: true },
      { id: 'spring', title: { es: 'Spring Boot', en: 'Spring Boot' }, help: { es: 'Guárdalo como src/main/resources/application-nimbo.yml y arranca con SPRING_PROFILES_ACTIVE=nimbo.', en: 'Save it as src/main/resources/application-nimbo.yml and start with SPRING_PROFILES_ACTIVE=nimbo.' }, passwords: true },
      { id: 'dotnet', title: { es: '.NET', en: '.NET' }, help: { es: 'Guárdalo como appsettings.Nimbo.json y arranca con ASPNETCORE_ENVIRONMENT=Nimbo.', en: 'Save it as appsettings.Nimbo.json and start with ASPNETCORE_ENVIRONMENT=Nimbo.' }, passwords: true },
    ],
  },
  {
    title: { es: 'Herramientas', en: 'Tools' },
    intro: { es: 'Solo para stacks con el emulador de AWS.', en: 'Only for stacks with the AWS emulator.' },
    formats: [
      { id: 'tofu', title: { es: 'OpenTofu / Terraform', en: 'OpenTofu / Terraform' }, help: { es: 'Déjalo junto a tus .tf: apunta el proveedor de AWS al emulador. Bórralo para volver a AWS real.', en: 'Drop it next to your .tf files: it points the AWS provider at the emulator. Delete it to go back to real AWS.' }, chaos: true, needsAws: true },
      { id: 'awscli', title: { es: 'AWS CLI', en: 'AWS CLI' }, help: { es: 'Añádelo a ~/.aws/config y usa aws --profile nimbo s3 ls.', en: 'Add it to ~/.aws/config and use aws --profile nimbo s3 ls.' }, chaos: true, needsAws: true },
    ],
  },
]

const ALL_FORMATS = EXPORT_GROUPS.flatMap((g) => g.formats)

/** Writes the files that connect something to a stack, and lets you copy or save each one. */
export function ExportPanel({ stack, t, lang, hasAws, onClose }: { stack: Stack; t: T; lang: Lang; hasAws: boolean; onClose: () => void }) {
  const [format, setFormat] = useState<ExportFormat>('compose')
  const [content, setContent] = useState('')
  const [filename, setFilename] = useState('')
  const [copied, setCopied] = useState(false)
  const [viaChaos, setViaChaos] = useState(false)
  const [keepData, setKeepData] = useState(false)
  const [passwords, setPasswords] = useState(false)
  const info = ALL_FORMATS.find((f) => f.id === format)!
  const es = lang === 'es'

  useEffect(() => {
    let alive = true
    api
      .stackExport(stack.id, format, { chaos: viaChaos && !!info.chaos, persist: keepData && !!info.keepData, secrets: passwords && !!info.passwords })
      .then((r) => {
        if (!alive) return
        setContent(r.content)
        setFilename(r.filename)
      })
      .catch((e) => {
        if (!alive) return
        setContent(e instanceof Error ? e.message : String(e))
        setFilename('')
      })
    return () => {
      alive = false
    }
  }, [stack.id, format, viaChaos, keepData, passwords, info.chaos, info.keepData, info.passwords])

  const save = () => {
    const url = URL.createObjectURL(new Blob([content], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <aside className="panel panel-wide" role="dialog" aria-label={`${stack.id} ${t('export')}`}>
      <div className="panel-head">
        <h3>
          {stack.id} · {es ? 'Archivos de configuración' : 'Configuration files'}
        </h3>
        <div>
          <button className="ghost" onClick={onClose}>
            {t('close')}
          </button>
        </div>
      </div>

      <div className="fmt-groups">
        {EXPORT_GROUPS.map((g) => (
          <section key={g.title.en} className="fmt-group">
            <h4>{g.title[lang]}</h4>
            <p className="muted">{g.intro[lang]}</p>
            <div className="tabs" role="tablist">
              {g.formats.map((f) => {
                const off = !!f.needsAws && !hasAws
                return (
                  <button key={f.id} role="tab" aria-selected={f.id === format} disabled={off} title={off ? (es ? 'Este stack no tiene el emulador de AWS' : 'This stack has no AWS emulator') : undefined} className={f.id === format ? 'tab on' : 'tab'} onClick={() => setFormat(f.id)}>
                    {f.title[lang]}
                  </button>
                )
              })}
            </div>
          </section>
        ))}
      </div>

      <p className="hint">{info.help[lang]}</p>
      <div className="fmt-options">
        {info.keepData && (
          <label className="field">
            <input type="checkbox" checked={keepData} onChange={(e) => setKeepData(e.target.checked)} />
            {es ? 'Conservar datos en volúmenes' : 'Keep data in volumes'}
          </label>
        )}
        {info.chaos && (
          <label className="field">
            <input type="checkbox" checked={viaChaos} onChange={(e) => setViaChaos(e.target.checked)} />
            {t('viaChaos')}
          </label>
        )}
        {info.passwords && (
          <label className="field" title={es ? 'Queda anotado en el diario, sin el valor' : 'It is noted in the journal, without the value'}>
            <input type="checkbox" checked={passwords} onChange={(e) => setPasswords(e.target.checked)} />
            {es ? 'Incluir las contraseñas reales' : 'Include the real passwords'}
          </label>
        )}
      </div>
      {info.passwords && passwords && (
        <p className="err">{es ? 'Este archivo contiene contraseñas: no lo subas a git ni lo compartas.' : 'This file contains passwords: keep it out of git and do not share it.'}</p>
      )}

      <div className="actions">
        <button
          className="primary"
          disabled={!filename}
          onClick={() =>
            copyText(content, () => {
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            })
          }
        >
          {copied ? t('copied') : t('copy')}
        </button>
        <button disabled={!filename} onClick={save}>
          {es ? 'Descargar' : 'Download'} {filename && <code>{filename}</code>}
        </button>
      </div>
      <pre>{content}</pre>
    </aside>
  )
}
