import { useEffect, useState } from 'react'
import { api, type ExportFormat, type Provider, type Stack } from './api'
import type { Key } from './i18n'

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

  useEffect(() => {
    let alive = true
    const load = () =>
      (panel.kind === 'logs' ? api.logs(panel.provider.id).then((r) => r.lines) : api.env(panel.provider.id).then((r) => r.dotenv))
        .then((v) => alive && setText(v))
        .catch((e) => alive && setText(e instanceof Error ? e.message : String(e)))
    load()
    const id = panel.kind === 'logs' ? setInterval(load, 2500) : undefined
    return () => {
      alive = false
      if (id) clearInterval(id)
    }
  }, [panel])

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
          <button className="ghost" onClick={onClose}>
            {t('close')}
          </button>
        </div>
      </div>
      <pre>{text || t('noLogs')}</pre>
    </aside>
  )
}

const FORMATS: ExportFormat[] = ['env', 'tofu', 'awscli']

/** The three ways to connect your own tools to a stack: variables, an OpenTofu override, an AWS CLI profile. */
export function ExportPanel({ stack, t, onClose }: { stack: Stack; t: T; onClose: () => void }) {
  const [format, setFormat] = useState<ExportFormat>('env')
  const [content, setContent] = useState('')
  const [filename, setFilename] = useState('')
  const [copied, setCopied] = useState(false)
  const [viaChaos, setViaChaos] = useState(false)

  useEffect(() => {
    let alive = true
    api
      .stackExport(stack.id, format, viaChaos)
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
  }, [stack.id, format, viaChaos])

  return (
    <aside className="panel" role="dialog" aria-label={`${stack.id} ${t('export')}`}>
      <div className="panel-head">
        <h3>
          {stack.id} · {t('export')}
        </h3>
        <div>
          <button
            className="ghost"
            onClick={() =>
              copyText(content, () => {
                setCopied(true)
                setTimeout(() => setCopied(false), 1500)
              })
            }
          >
            {copied ? t('copied') : t('copy')}
          </button>
          <button className="ghost" onClick={onClose}>
            {t('close')}
          </button>
        </div>
      </div>
      <div className="tabs" role="tablist">
        {FORMATS.map((f) => (
          <button key={f} role="tab" aria-selected={f === format} className={f === format ? 'tab on' : 'tab'} onClick={() => setFormat(f)}>
            {t(`fmt_${f}` as Key)}
          </button>
        ))}
      </div>
      <label className="field export-chaos">
        <input type="checkbox" checked={viaChaos} onChange={(e) => setViaChaos(e.target.checked)} />
        {t('viaChaos')}
      </label>
      {filename && (
        <p className="hint">
          {t('exportHint')} <code>{filename}</code>
        </p>
      )}
      <pre>{content}</pre>
    </aside>
  )
}
