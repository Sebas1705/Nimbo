import { useEffect, useState } from 'react'
import { api, kube, podTone, type KubePod, type KubeSample } from './api'
import type { Key, Lang } from './i18n'
import './screens.css'
import { Card, ConfirmButton, Drawer, EmptyState, Feedback, Notice, Pill, pick, useAction, useCopy, useLoad } from './ui'

type T = (k: Key) => string

const PUBLISHED_NODEPORTS = new Set([30080, 30081, 30082, 30083, 30084])
const SYSTEM = new Set(['kube-system', 'kube-public', 'kube-node-lease'])

export function KubeView({ t, lang }: { t: T; lang: Lang }) {
  const { data, error: loadError, reload } = useLoad(kube.overview, 3000)
  const [namespace, setNamespace] = useState('default')
  const [logs, setLogs] = useState<KubePod | null>(null)
  const starter = useAction()
  const changes = useAction()

  const available = data?.available === true
  const starting = starter.busy
  // The cluster takes a while after the container is up: keep saying so until the API answers.
  const [waiting, setWaiting] = useState(false)
  useEffect(() => {
    if (available) setWaiting(false)
  }, [available])

  const start = () =>
    starter
      .run('', async () => {
        await api.start('k3s')
        setWaiting(true)
      })
      .then(reload)

  const act = (fn: () => Promise<unknown>) =>
    changes.run('', async () => {
      try {
        await fn()
      } finally {
        reload()
      }
    })

  if (!data) return <Feedback working={loadError ? '' : t('loading')} error={loadError} />

  if (!data.available) {
    const busy = starting || waiting
    return (
      <div className="kube">
        <Notice>
          <span className="notice-line">
            {busy ? t('kubeStarting') : (data.reason ?? '')}
            <button className="primary" disabled={busy} onClick={start}>
              {busy ? '…' : t('startKube')}
            </button>
          </span>
        </Notice>
        <Feedback error={starter.error || loadError} />
      </div>
    )
  }

  const inScope = (ns: string) => namespace === '*' || ns === namespace
  const deployments = data.deployments.filter((d) => inScope(d.namespace))
  const pods = data.pods.filter((p) => inScope(p.namespace))
  const services = data.services.filter((s) => inScope(s.namespace))
  const nodesReady = data.nodes.filter((n) => n.ready).length

  const remove = (kind: 'pod' | 'deployment' | 'service', ns: string, name: string, label: string) => (
    <ConfirmButton
      lang={lang}
      label={t('delete')}
      question={pick(lang, `¿Borrar ${label} ${name}?`, `Delete ${label} ${name}?`)}
      confirmLabel={pick(lang, 'Sí, borrar', 'Yes, delete')}
      disabled={changes.busy}
      onConfirm={() => act(() => kube.remove(kind, ns, name))}
    />
  )

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
      <Feedback error={changes.error || loadError} />

      <section className="cloud cloud-other kube-block">
        <h2>{t('deployments')}</h2>
        {deployments.length === 0 && <EmptyState title={pick(lang, 'Sin deployments', 'No deployments')} body={t('kubeEmptyDeploy')} />}
        <ul className="kube-list">
          {deployments.map((d) => (
            <li key={`${d.namespace}/${d.name}`} className="kube-row">
              <span className="kube-name">
                {d.name} <small>{d.namespace}</small>
              </span>
              <Pill tone={d.ready === d.desired ? 'ok' : 'warn'}>
                {d.ready}/{d.desired}
              </Pill>
              {!SYSTEM.has(d.namespace) && (
                <span className="kube-actions">
                  <button className="ghost" aria-label="scale down" disabled={d.desired <= 0} onClick={() => act(() => kube.scale(d.namespace, d.name, d.desired - 1))}>
                    −
                  </button>
                  <button className="ghost" aria-label="scale up" onClick={() => act(() => kube.scale(d.namespace, d.name, d.desired + 1))}>
                    +
                  </button>
                  {remove('deployment', d.namespace, d.name, 'deployment')}
                </span>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section className="cloud cloud-other kube-block">
        <h2>{t('pods')}</h2>
        {pods.length === 0 && <EmptyState title={pick(lang, 'Sin pods', 'No pods')} body={t('kubeEmptyPods')} />}
        <ul className="kube-list">
          {pods.map((p) => (
            <li key={`${p.namespace}/${p.name}`} className="kube-row">
              <span className="kube-name">
                {p.name} <small>{p.namespace}</small>
              </span>
              <Pill tone={podTone(p.status)}>{p.status}</Pill>
              <small>
                {p.ready} · {p.restarts} {t('restarts')}
              </small>
              <span className="kube-actions">
                <button className="ghost" onClick={() => setLogs(p)}>
                  {t('logs')}
                </button>
                {!SYSTEM.has(p.namespace) && remove('pod', p.namespace, p.name, 'pod')}
              </span>
            </li>
          ))}
        </ul>
      </section>

      <section className="cloud cloud-other kube-block">
        <h2>{t('k8sServices')}</h2>
        {services.length === 0 && (
          <EmptyState title={pick(lang, 'Sin servicios', 'No services')} body={pick(lang, 'Aparecerán cuando apliques un manifiesto con un Service.', 'They will appear once you apply a manifest with a Service.')} />
        )}
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
              {!SYSTEM.has(s.namespace) && s.name !== 'kubernetes' && <span className="kube-actions">{remove('service', s.namespace, s.name, 'service')}</span>}
            </li>
          ))}
        </ul>
      </section>

      <ApplyBox t={t} onDone={reload} />
      {logs && <KubeLogs key={`${logs.namespace}/${logs.name}`} pod={logs} t={t} lang={lang} onClose={() => setLogs(null)} />}
    </div>
  )
}

function ApplyBox({ t, onDone }: { t: T; onDone: () => void }) {
  const [samples, setSamples] = useState<KubeSample[]>([])
  const [manifest, setManifest] = useState('')
  const [output, setOutput] = useState('')
  const [failed, setFailed] = useState(false)
  const apply = useAction()
  const config = useAction()
  const { copied, copy } = useCopy(1800)

  useEffect(() => {
    kube.samples().then(setSamples).catch(() => {})
  }, [])

  // kubectl's answer keeps its line breaks, so it goes in a block, not in a one-line message.
  const run = () =>
    apply.run('', async () => {
      try {
        const r = await kube.apply(manifest)
        setOutput(r.output)
        setFailed(false)
      } catch (e) {
        setOutput(e instanceof Error ? e.message : String(e))
        setFailed(true)
      } finally {
        onDone()
      }
    })

  const copyKubeconfig = () =>
    config.run('', async () => {
      const cfg = await kube.kubeconfig()
      await copy(cfg.content)
    })

  return (
    <div className="apply-box">
      <Card title={t('applyTitle')}>
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
        <Feedback error={config.error} />
        <textarea value={manifest} onChange={(e) => setManifest(e.target.value)} placeholder={t('manifestPlaceholder')} spellCheck={false} aria-label="manifest" />
        <div className="actions">
          <button className="primary" disabled={apply.busy || manifest.trim() === ''} onClick={run}>
            {apply.busy ? '…' : t('apply2')}
          </button>
        </div>
        {output && <pre className={failed ? 'mini err-box' : 'mini'}>{output}</pre>}
      </Card>
    </div>
  )
}

function KubeLogs({ pod, t, lang, onClose }: { pod: KubePod; t: T; lang: Lang; onClose: () => void }) {
  const { data, error } = useLoad(() => kube.logs(pod.namespace, pod.name).then((r) => r.output), 2500)

  return (
    <Drawer title={`${pod.name} · ${t('logs')}`} label={pod.name} lang={lang} onClose={onClose}>
      <pre>{data || error || t('noLogs')}</pre>
    </Drawer>
  )
}
