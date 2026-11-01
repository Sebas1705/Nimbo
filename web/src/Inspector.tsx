import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { ApiError, formatBytes, inspect, type ObjectList, type PubSub, type QueueMessage } from './api'
import type { Key } from './i18n'

type T = (k: Key) => string

interface Polled<V> {
  data: V | null
  error: ApiError | null
}

/** Re-runs `load` every `ms` while mounted; keeps the last good data when a refresh fails. */
function usePolled<V>(load: () => Promise<V>, ms = 4000): Polled<V> & { reload: () => void } {
  const [state, setState] = useState<Polled<V>>({ data: null, error: null })
  const loadRef = useRef(load)
  loadRef.current = load

  const reload = useCallback(() => {
    loadRef
      .current()
      .then((data) => setState({ data, error: null }))
      .catch((e) => setState((prev) => ({ data: prev.data, error: e instanceof ApiError ? e : new ApiError(0, String(e)) })))
  }, [])

  useEffect(() => {
    reload()
    const id = setInterval(reload, ms)
    return () => clearInterval(id)
  }, [reload, ms])

  return { ...state, reload }
}

export function InspectorView({ t }: { t: T }) {
  return (
    <div className="inspector">
      <S3Section t={t} />
      <SqsSection t={t} />
      <DynamoSection t={t} />
      <PubSubSection t={t} />
    </div>
  )
}

function Section({ title, cloud, error, t, children }: { title: string; cloud: string; error: ApiError | null; t: T; children: ReactNode }) {
  return (
    <section className={`cloud cloud-${cloud} insp`}>
      <h2>{title}</h2>
      {error && error.status === 503 ? (
        <p className="muted">{t('notRunning')}</p>
      ) : error ? (
        <p className="err">{error.message}</p>
      ) : null}
      {(!error || error.status !== 503) && children}
    </section>
  )
}

function Empty({ t }: { t: T }) {
  return <p className="muted">{t('empty')}</p>
}

function Pick({ items, selected, onSelect }: { items: Array<{ id: string; label: string; meta?: string }>; selected: string | null; onSelect: (id: string) => void }) {
  return (
    <ul className="pick">
      {items.map((i) => (
        <li key={i.id}>
          <button className={i.id === selected ? 'row on' : 'row'} onClick={() => onSelect(i.id)}>
            <span>{i.label}</span>
            {i.meta && <small>{i.meta}</small>}
          </button>
        </li>
      ))}
    </ul>
  )
}

function S3Section({ t }: { t: T }) {
  const buckets = usePolled(inspect.buckets)
  const [bucket, setBucket] = useState<string | null>(null)
  const objects = usePolled<ObjectList | null>(() => (bucket ? inspect.objects(bucket) : Promise.resolve(null)))
  useEffect(() => objects.reload(), [bucket, objects.reload])
  const [key, setKey] = useState<string | null>(null)
  const [preview, setPreview] = useState('')

  useEffect(() => {
    setKey(null)
    setPreview('')
  }, [bucket])

  useEffect(() => {
    if (!bucket || !key) return
    let alive = true
    inspect
      .preview(bucket, key)
      .then((p) => alive && setPreview(p.text + (p.truncated ? '\n…' : '')))
      .catch((e) => alive && setPreview(e instanceof Error ? e.message : String(e)))
    return () => {
      alive = false
    }
  }, [bucket, key])

  return (
    <Section title="S3 · Amazon" cloud="aws" error={buckets.error} t={t}>
      {buckets.data && buckets.data.length === 0 && <Empty t={t} />}
      <div className="split">
        <Pick items={(buckets.data ?? []).map((b) => ({ id: b.name, label: b.name }))} selected={bucket} onSelect={setBucket} />
        {bucket && (
          <div className="detail">
            {objects.error && <p className="err">{objects.error.message}</p>}
            {objects.data && objects.data.objects.length === 0 && <Empty t={t} />}
            <ul className="pick">
              {objects.data?.objects.map((o) => (
                <li key={o.key}>
                  <button className={o.key === key ? 'row on' : 'row'} onClick={() => setKey(o.key)}>
                    <span>{o.key}</span>
                    <small>{formatBytes(o.size)}</small>
                  </button>
                </li>
              ))}
            </ul>
            {objects.data?.truncated && <p className="hint">…</p>}
            {key && <pre className="mini">{preview}</pre>}
          </div>
        )}
      </div>
    </Section>
  )
}

function SqsSection({ t }: { t: T }) {
  const queues = usePolled(inspect.queues)
  const [queue, setQueue] = useState<string | null>(null)
  const messages = usePolled<QueueMessage[]>(() => (queue ? inspect.messages(queue).then((r) => r.messages) : Promise.resolve([])))
  useEffect(() => messages.reload(), [queue, messages.reload])

  return (
    <Section title="SQS · Amazon" cloud="aws" error={queues.error} t={t}>
      {queues.data && queues.data.length === 0 && <Empty t={t} />}
      <div className="split">
        <Pick
          items={(queues.data ?? []).map((q) => ({
            id: q.name,
            label: q.name,
            meta: `${q.messages ?? '?'} · ${q.inFlight ?? '?'} ${t('inFlight')}`,
          }))}
          selected={queue}
          onSelect={setQueue}
        />
        {queue && (
          <div className="detail">
            <p className="hint">{t('peekNote')}</p>
            {messages.error && <p className="err">{messages.error.message}</p>}
            {messages.data && messages.data.length === 0 && <Empty t={t} />}
            {messages.data?.map((m) => (
              <div key={m.id} className="msg">
                <pre className="mini">{m.body}</pre>
                <small>
                  {m.id.slice(0, 8)} · ×{m.receiveCount ?? '?'}
                </small>
              </div>
            ))}
          </div>
        )}
      </div>
    </Section>
  )
}

function DynamoSection({ t }: { t: T }) {
  const tables = usePolled(inspect.tables)
  const [table, setTable] = useState<string | null>(null)
  const items = usePolled(() => (table ? inspect.items(table) : Promise.resolve(null)))
  useEffect(() => items.reload(), [table, items.reload])

  return (
    <Section title="DynamoDB · Amazon" cloud="aws" error={tables.error} t={t}>
      {tables.data && tables.data.length === 0 && <Empty t={t} />}
      <div className="split">
        <Pick
          items={(tables.data ?? []).map((x) => ({ id: x.name, label: x.name, meta: `${x.items ?? '?'} · ${x.keys.join(', ')}` }))}
          selected={table}
          onSelect={setTable}
        />
        {table && (
          <div className="detail">
            {items.error && <p className="err">{items.error.message}</p>}
            {items.data && items.data.items.length === 0 && <Empty t={t} />}
            {items.data && items.data.items.length > 0 && <pre className="mini">{JSON.stringify(items.data.items, null, 2)}</pre>}
          </div>
        )}
      </div>
    </Section>
  )
}

function PubSubSection({ t }: { t: T }) {
  const [project, setProject] = useState('floci-local')
  const pubsub = usePolled<PubSub>(() => inspect.pubsub(project))
  useEffect(() => pubsub.reload(), [project, pubsub.reload])

  return (
    <Section title="Pub/Sub · Google Cloud" cloud="gcp" error={pubsub.error} t={t}>
      <label className="field">
        {t('project')}
        <input value={project} onChange={(e) => setProject(e.target.value.trim())} aria-label={t('project')} />
      </label>
      {pubsub.data && pubsub.data.topics.length === 0 && pubsub.data.subscriptions.length === 0 && <Empty t={t} />}
      <div className="split">
        <ul className="pick">
          {pubsub.data?.topics.map((x) => (
            <li key={x.name}>
              <span className="row static">
                <span>{x.name}</span>
                <small>{t('topic')}</small>
              </span>
            </li>
          ))}
          {pubsub.data?.subscriptions.map((x) => (
            <li key={x.name}>
              <span className="row static">
                <span>{x.name}</span>
                <small>
                  {t('subscription')} → {x.topic ?? '?'}
                </small>
              </span>
            </li>
          ))}
        </ul>
      </div>
    </Section>
  )
}
