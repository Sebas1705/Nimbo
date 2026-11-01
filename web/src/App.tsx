import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ApiError,
  api,
  countRunning,
  groupByCloud,
  failures,
  tokenStore,
  type DockerStatus,
  type ExportFormat,
  type Provider,
  type Stack,
  type StackList,
} from './api'
import { ChaosView } from './Chaos'
import { FidelityView } from './Fidelity'
import { KubeView } from './Kube'
import { LabsView } from './Labs'
import { TracesView } from './Traces'
import { InspectorView } from './Inspector'
import { detectLang, translate, type Key, type Lang } from './i18n'

type View = 'infra' | 'inspector' | 'chaos' | 'kube' | 'traces' | 'fidelity' | 'labs'

const VIEWS: Array<{ id: View; label: Key }> = [
  { id: 'infra', label: 'viewInfra' },
  { id: 'inspector', label: 'viewInspector' },
  { id: 'chaos', label: 'viewChaos' },
  { id: 'kube', label: 'viewKube' },
  { id: 'traces', label: 'viewTraces' },
  { id: 'fidelity', label: 'viewFidelity' },
  { id: 'labs', label: 'viewLabs' },
]

type Panel = { kind: 'logs' | 'env'; provider: Provider } | null

export function App() {
  const [lang, setLang] = useState<Lang>(() => detectLang())
  const t = useCallback((k: Key) => translate(lang, k), [lang])

  const [needsToken, setNeedsToken] = useState(false)
  const [docker, setDocker] = useState<DockerStatus | null>(null)
  const [providers, setProviders] = useState<Provider[]>([])
  const [busy, setBusy] = useState<Set<string>>(new Set())
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [panel, setPanel] = useState<Panel>(null)
  const [stackList, setStackList] = useState<StackList>({ stacks: [], problems: [] })
  const [stackBusy, setStackBusy] = useState<string | null>(null)
  const [stackErrors, setStackErrors] = useState<Record<string, string>>({})
  const [exporting, setExporting] = useState<Stack | null>(null)
  const [view, setView] = useState<View>('infra')
  const [version, setVersion] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [d, p, s] = await Promise.all([api.docker(), api.providers(), api.stacks()])
      setDocker(d)
      setProviders(p)
      setStackList(s)
      setNeedsToken(false)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setNeedsToken(true)
    }
  }, [])

  useEffect(() => {
    api.health().then((h) => setVersion(h.version ?? null)).catch(() => {})
    refresh()
    const id = setInterval(refresh, 3000)
    return () => clearInterval(id)
  }, [refresh])

  const act = async (p: Provider, action: 'start' | 'stop' | 'remove') => {
    setBusy((b) => new Set(b).add(p.id))
    setErrors((e) => ({ ...e, [p.id]: '' }))
    try {
      await api[action](p.id)
    } catch (e) {
      setErrors((prev) => ({ ...prev, [p.id]: e instanceof Error ? e.message : String(e) }))
    } finally {
      setBusy((b) => {
        const n = new Set(b)
        n.delete(p.id)
        return n
      })
      refresh()
    }
  }

  const runStack = async (s: Stack, action: 'stackUp' | 'stackDown') => {
    setStackBusy(s.id)
    setStackErrors((e) => ({ ...e, [s.id]: '' }))
    try {
      const result = await api[action](s.id)
      setStackErrors((e) => ({ ...e, [s.id]: failures(result).join(' · ') }))
    } catch (e) {
      setStackErrors((prev) => ({ ...prev, [s.id]: e instanceof Error ? e.message : String(e) }))
    } finally {
      setStackBusy(null)
      refresh()
    }
  }

  const groups = useMemo(() => groupByCloud(providers), [providers])
  const running = countRunning(providers)

  if (needsToken) return <TokenGate t={t} onDone={refresh} />

  return (
    <div className="shell">
      <header className="top">
        <div className="brand">
          <Logo />
          <div>
            <h1>Nimbo</h1>
            <p>{t('tagline')}</p>
          </div>
        </div>
        <div className="top-right">
          <span className="chip" data-ok={docker?.available ?? false}>
            <i className="dot" />
            {docker?.available ? `${t('dockerUp')} ${docker.version ?? ''}` : t('dockerDown')}
          </span>
          <span className="chip">
            <b>{running}</b>&nbsp;{t('running')}
          </span>
          <button className="ghost" onClick={() => setLang(lang === 'es' ? 'en' : 'es')} aria-label="language">
            {lang === 'es' ? 'EN' : 'ES'}
          </button>
        </div>
      </header>

      <nav className="views" role="tablist">
        {VIEWS.map((v) => (
          <button key={v.id} role="tab" aria-selected={view === v.id} className={view === v.id ? 'tab on' : 'tab'} onClick={() => setView(v.id)}>
            {t(v.label)}
          </button>
        ))}
      </nav>

      {docker && !docker.available && (
        <div className="banner" role="alert">
          {t('dockerHint')}
        </div>
      )}

      {view === 'inspector' && <InspectorView t={t} />}
      {view === 'chaos' && <ChaosView t={t} />}
      {view === 'kube' && <KubeView t={t} />}
      {view === 'traces' && <TracesView t={t} />}
      {view === 'fidelity' && <FidelityView t={t} />}
      {view === 'labs' && <LabsView t={t} lang={lang} />}

      <main hidden={view !== 'infra'}>
        {stackList.stacks.length > 0 && (
          <section className="stacks" aria-label={t('stacks')}>
            <h2>{t('stacks')}</h2>
            <div className="stack-row">
              {stackList.stacks.map((s) => (
                <article key={s.id} className="stack" data-state={s.state}>
                  <div className="card-head">
                    <h3>{s.id}</h3>
                    <span className={`pill stack-${s.state}`}>
                      <i className="dot" />
                      {t(`stack_${s.state}` as Key)} · {s.running}/{s.total}
                    </span>
                  </div>
                  <p className="desc">{s.description}</p>
                  <div className="meta">
                    {s.providers.map((id) => (
                      <code key={id}>{id}</code>
                    ))}
                  </div>
                  {stackErrors[s.id] && <p className="err">{stackErrors[s.id]}</p>}
                  <div className="actions">
                    <button
                      className="primary"
                      disabled={stackBusy !== null || !docker?.available || s.state === 'running'}
                      onClick={() => runStack(s, 'stackUp')}
                    >
                      {stackBusy === s.id ? '…' : t('up')}
                    </button>
                    <button className="ghost" disabled={stackBusy !== null || s.running === 0} onClick={() => runStack(s, 'stackDown')}>
                      {t('down')}
                    </button>
                    <button className="ghost" onClick={() => setExporting(s)}>
                      {t('export')}
                    </button>
                  </div>
                </article>
              ))}
            </div>
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
        )}
        {providers.length === 0 && <p className="muted">{t('loading')}</p>}
        {groups.map(([cloud, list]) => (
          <section key={cloud} className={`cloud cloud-${cloud}`}>
            <h2>{t(`cloud_${cloud}` as Key)}</h2>
            <div className="grid">
              {list.map((p) => (
                <article key={p.id} className="card" data-state={p.state}>
                  <div className="card-head">
                    <h3>{p.name}</h3>
                    <span className={`pill ${p.state}`}>
                      <i className="dot" />
                      {t(`state_${p.state}` as Key)}
                    </span>
                  </div>
                  <p className="desc">{p.description}</p>
                  <div className="meta">
                    <code>{p.image}</code>
                    {p.ports.map((port) => (
                      <code key={port}>:{port}</code>
                    ))}
                  </div>
                  {errors[p.id] && <p className="err">{errors[p.id]}</p>}
                  <div className="actions">
                    {p.state === 'running' ? (
                      <button disabled={busy.has(p.id)} onClick={() => act(p, 'stop')}>
                        {t('stop')}
                      </button>
                    ) : (
                      <button className="primary" disabled={busy.has(p.id) || !docker?.available} onClick={() => act(p, 'start')}>
                        {t('start')}
                      </button>
                    )}
                    <button className="ghost" disabled={p.state === 'not_created'} onClick={() => setPanel({ kind: 'logs', provider: p })}>
                      {t('logs')}
                    </button>
                    <button className="ghost" onClick={() => setPanel({ kind: 'env', provider: p })}>
                      {t('env')}
                    </button>
                    {p.state === 'stopped' && (
                      <button className="ghost danger" disabled={busy.has(p.id)} onClick={() => act(p, 'remove')}>
                        {t('remove')}
                      </button>
                    )}
                  </div>
                </article>
              ))}
            </div>
          </section>
        ))}
      </main>

      <footer className="foot">
        Nimbo {version ? `v${version}` : 'dev'} ·{' '}
        <a href="https://github.com/Sebas1705/Nimbo" target="_blank" rel="noreferrer noopener">GitHub</a>
      </footer>

      {panel && <SidePanel panel={panel} t={t} onClose={() => setPanel(null)} />}
      {exporting && <ExportPanel stack={exporting} t={t} onClose={() => setExporting(null)} />}
    </div>
  )
}

function SidePanel({ panel, t, onClose }: { panel: NonNullable<Panel>; t: (k: Key) => string; onClose: () => void }) {
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

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked */
    }
  }

  return (
    <aside className="panel" role="dialog" aria-label={panel.provider.name}>
      <div className="panel-head">
        <h3>
          {panel.provider.name} · {t(panel.kind)}
        </h3>
        <div>
          <button className="ghost" onClick={copy}>
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

function ExportPanel({ stack, t, onClose }: { stack: Stack; t: (k: Key) => string; onClose: () => void }) {
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

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked */
    }
  }

  return (
    <aside className="panel" role="dialog" aria-label={`${stack.id} ${t('export')}`}>
      <div className="panel-head">
        <h3>
          {stack.id} · {t('export')}
        </h3>
        <div>
          <button className="ghost" onClick={copy}>
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

function TokenGate({ t, onDone }: { t: (k: Key) => string; onDone: () => void }) {
  const [value, setValue] = useState('')
  const [bad, setBad] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    tokenStore.set(value.trim())
    try {
      await api.providers()
      setBad(false)
      onDone()
    } catch {
      setBad(true)
    }
  }

  return (
    <div className="gate">
      <form onSubmit={submit} className="card">
        <Logo />
        <h3>{t('tokenTitle')}</h3>
        <p className="desc">{t('tokenBody')}</p>
        <input type="password" autoFocus value={value} onChange={(e) => setValue(e.target.value)} aria-label="token" />
        {bad && <p className="err">{t('unauthorized')}</p>}
        <button className="primary" type="submit">
          {t('tokenSave')}
        </button>
      </form>
    </div>
  )
}

function Logo() {
  return (
    <svg width="36" height="36" viewBox="0 0 32 32" aria-hidden="true">
      <defs>
        <linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#9db4ff" />
          <stop offset="1" stopColor="#6ee7c8" />
        </linearGradient>
      </defs>
      <path fill="url(#g)" d="M9 24a6 6 0 0 1-.6-12A8 8 0 0 1 24 10.5 6.5 6.5 0 0 1 23.5 24Z" />
    </svg>
  )
}
