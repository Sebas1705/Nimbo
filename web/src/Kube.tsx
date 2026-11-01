import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, kube, podTone, type KubeOverview, type KubePod, type KubeSample } from './api'
import type { Key } from './i18n'

type T = (k: Key) => string

const PUBLISHED_NODEPORTS = new Set([30080, 30081, 30082, 30083, 30084])
const SYSTEM = new Set(['kube-system', 'kube-public', 'kube-node-lease'])

export function KubeView({ t }: { t: T }) {
  const [data, setData] = useState<KubeOverview | null>(null)
  const [namespace, setNamespace] = useState('default')
  const [starting, setStarting] = useState(false)
  const [error, setError] = useState('')
  const [logs, setLogs] = useState<KubePod | null>(null)

  const refresh = useCallback(() => {
    kube
      .overview()
      .then((o) => {
        setData(o)
        setError('')
        if (o.available) setStarting(false)
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, 3000)
    return () => clearInterval(id)
  }, [refresh])

  const start = async () => {
    setStarting(true)
    try {
      await api.start('k3s')
    } catch (e) {
      setStarting(false)
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const act = async (fn: () => Promise<unknown>) => {
    setError('')
    try {
      await fn()
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
    } finally {
      refresh()
    }
  }

  if (!data) return <p className="muted">{error || '…'}</p>

  if (!data.available) {
    return (
      <div className="kube">
        <div className="banner" role="alert">
          {starting ? t('kubeStarting') : (data.reason ?? '')}{' '}
          <button className="primary" disabled={starting} onClick={start}>
            {starting ? '…' : t('startKube')}
          </button>
        </div>
        {error && <p className="err">{error}</p>}
      </div>
    )
  }

  const inScope = (ns: string) => namespace === '*' || ns === namespace
  const deployments = data.deployments.filter((d) => inScope(d.namespace))
  const pods = data.pods.filter((p) => inScope(p.namespace))
  const services = data.services.filter((s) => inScope(s.namespace))
  const nodesReady = data.nodes.filter((n) => n.ready).length

  return (
    <div className="kube">
      <div className="chaos-head">
        <div className="top-right">
          <span className="chip" data-ok={nodesReady > 0}>
            <i className="dot" />
            {nodesReady}/{data.nodes.length} {t('nodeReady')} · {data.version}
          </span>
          <label className="field">
            {t('namespace')}
            <select value={namespace} onChange={(e) => setNamespace(e.target.value)} aria-label={t('namespace')}>
              <option value="*">{t('allNamespaces')}</option>
              {data.namespaces.map((n) => (
                <option key={n} value={n}>
                  {n}
                  {SYSTEM.has(n) ? ' ·' : ''}
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>
      {error && <p className="err">{error}</p>}

      <section className="cloud cloud-other kube-block">
        <h2>{t('deployments')}</h2>
        {deployments.length === 0 && <p className="empty">{t('kubeEmptyDeploy')}</p>}
        <ul className="kube-list">
          {deployments.map((d) => (
            <li key={`${d.namespace}/${d.name}`} className="kube-row">
              <span className="kube-name">
                {d.name} <small>{d.namespace}</small>
              </span>
              <span className={`pill ${d.ready === d.desired ? 'running' : 'stopped'}`}>
                <i className="dot" />
                {d.ready}/{d.desired}
              </span>
              {!SYSTEM.has(d.namespace) && (
                <span className="kube-actions">
                  <button className="ghost" aria-label="scale down" disabled={d.desired <= 0} onClick={() => act(() => kube.scale(d.namespace, d.name, d.desired - 1))}>
                    −
                  </button>
                  <button className="ghost" aria-label="scale up" onClick={() => act(() => kube.scale(d.namespace, d.name, d.desired + 1))}>
                    +
                  </button>
                  <button className="ghost danger" onClick={() => act(() => kube.remove('deployment', d.namespace, d.name))}>
                    {t('delete')}
                  </button>
                </span>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section className="cloud cloud-other kube-block">
        <h2>{t('pods')}</h2>
        {pods.length === 0 && <p className="empty">{t('kubeEmptyPods')}</p>}
        <ul className="kube-list">
          {pods.map((p) => (
            <li key={`${p.namespace}/${p.name}`} className="kube-row">
              <span className="kube-name">
                {p.name} <small>{p.namespace}</small>
              </span>
              <span className={`pill pod-${podTone(p.status)}`}>
                <i className="dot" />
                {p.status}
              </span>
              <small>
                {p.ready} · {p.restarts} {t('restarts')}
              </small>
              <span className="kube-actions">
                <button className="ghost" onClick={() => setLogs(p)}>
                  {t('logs')}
                </button>
                {!SYSTEM.has(p.namespace) && (
                  <button className="ghost danger" onClick={() => act(() => kube.remove('pod', p.namespace, p.name))}>
                    {t('delete')}
                  </button>
                )}
              </span>
            </li>
          ))}
        </ul>
      </section>

      <section className="cloud cloud-other kube-block">
        <h2>{t('k8sServices')}</h2>
        <ul className="kube-list">
          {services.map((s) => (
            <li key={`${s.namespace}/${s.name}`} className="kube-row">
              <span className="kube-name">
                {s.name} <small>{s.namespace}</small>
              </span>
              <code>{s.type}</code>
              <span className="kube-ports">
                {s.ports.map((p, i) => {
                  const node = s.nodePorts[i]
                  return node && PUBLISHED_NODEPORTS.has(node) ? (
                    <a key={p} href={`http://localhost:${node}`} target="_blank" rel="noreferrer noopener">
                      {p}
                    </a>
                  ) : (
                    <code key={p}>{p}</code>
                  )
                })}
              </span>
              {!SYSTEM.has(s.namespace) && s.name !== 'kubernetes' && (
                <span className="kube-actions">
                  <button className="ghost danger" onClick={() => act(() => kube.remove('service', s.namespace, s.name))}>
                    {t('delete')}
                  </button>
                </span>
              )}
            </li>
          ))}
        </ul>
      </section>

      <ApplyBox t={t} onDone={refresh} />
      {logs && <KubeLogs pod={logs} t={t} onClose={() => setLogs(null)} />}
    </div>
  )
}

function ApplyBox({ t, onDone }: { t: T; onDone: () => void }) {
  const [samples, setSamples] = useState<KubeSample[]>([])
  const [manifest, setManifest] = useState('')
  const [output, setOutput] = useState('')
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    kube.samples().then(setSamples).catch(() => {})
  }, [])

  const apply = async () => {
    setBusy(true)
    try {
      const r = await kube.apply(manifest)
      setOutput(r.output)
      setFailed(false)
    } catch (e) {
      setOutput(e instanceof Error ? e.message : String(e))
      setFailed(true)
    } finally {
      setBusy(false)
      onDone()
    }
  }

  const copyKubeconfig = async () => {
    try {
      const cfg = await kube.kubeconfig()
      await navigator.clipboard.writeText(cfg.content)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch (e) {
      setOutput(e instanceof Error ? e.message : String(e))
      setFailed(true)
    }
  }

  return (
    <section className="kube-block apply-box">
      <h2>{t('applyTitle')}</h2>
      <div className="actions">
        <select
          aria-label={t('sampleLabel')}
          value=""
          onChange={(e) => {
            const s = samples.find((x) => x.id === e.target.value)
            if (s) setManifest(s.manifest)
          }}
        >
          <option value="">{t('sampleLabel')}…</option>
          {samples.map((s) => (
            <option key={s.id} value={s.id}>
              {s.title}
            </option>
          ))}
        </select>
        <button className="ghost" onClick={copyKubeconfig} title={t('kubeconfigNote')}>
          {copied ? t('copied') : t('copyKubeconfig')}
        </button>
      </div>
      <textarea value={manifest} onChange={(e) => setManifest(e.target.value)} placeholder={t('manifestPlaceholder')} spellCheck={false} aria-label="manifest" />
      <div className="actions">
        <button className="primary" disabled={busy || manifest.trim() === ''} onClick={apply}>
          {busy ? '…' : t('apply2')}
        </button>
      </div>
      {output && <pre className={failed ? 'mini err-box' : 'mini'}>{output}</pre>}
    </section>
  )
}

function KubeLogs({ pod, t, onClose }: { pod: KubePod; t: T; onClose: () => void }) {
  const [text, setText] = useState('')

  useEffect(() => {
    let alive = true
    const load = () =>
      kube
        .logs(pod.namespace, pod.name)
        .then((r) => alive && setText(r.output))
        .catch((e) => alive && setText(e instanceof Error ? e.message : String(e)))
    load()
    const id = setInterval(load, 2500)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [pod])

  return (
    <aside className="panel" role="dialog" aria-label={pod.name}>
      <div className="panel-head">
        <h3>
          {pod.name} · {t('logs')}
        </h3>
        <button className="ghost" onClick={onClose}>
          {t('close')}
        </button>
      </div>
      <pre>{text || t('noLogs')}</pre>
    </aside>
  )
}
