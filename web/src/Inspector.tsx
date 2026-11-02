import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ApiError, formatBytes, inspect, type ObjectList, type PubSub, type QueueMessage } from './api'
import type { Key, Lang } from './i18n'
import './screens.css'
import { EmptyState, Feedback, Field, Notice, pick, useLoad } from './ui'

type T = (k: Key) => string

interface Ctx {
  t: T
  lang: Lang
}

/** Polls an emulator every few seconds. `down` is true when the last answer was a 503: that emulator is not running. */
function usePolled<V>(load: () => Promise<V>, ms = 4000) {
  const down = useRef(false)
  const polled = useLoad(
    () =>
      load().then(
        (v) => {
          down.current = false
          return v
        },
        (e) => {
          down.current = e instanceof ApiError && e.status === 503
          throw e
        },
      ),
    ms,
  )
  return { ...polled, down: down.current && !!polled.error }
}

export function InspectorView(ctx: Ctx) {
  return (
    <div className="inspector">
      <S3Section {...ctx} />
      <SqsSection {...ctx} />
      <DynamoSection {...ctx} />
      <PubSubSection {...ctx} />
    </div>
  )
}

function Section({ title, cloud, error, down, t, children }: { title: string; cloud: string; error: string; down: boolean; t: T; children: ReactNode }) {
  return (
    <section className={`cloud cloud-${cloud} insp`}>
      <h2>{title}</h2>
      {down ? <Notice>{t('notRunning')}</Notice> : <Feedback error={error} />}
      {!down && children}
    </section>
  )
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

function S3Section({ t, lang }: Ctx) {
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
    <Section title="S3 · Amazon" cloud="aws" error={buckets.error} down={buckets.down} t={t}>
      {buckets.data && buckets.data.length === 0 && (
        <EmptyState
          title={pick(lang, 'Aún no hay buckets', 'No buckets yet')}
          body={pick(lang, 'Crea uno desde tu aplicación o con la CLI de AWS apuntando a este emulador.', 'Create one from your app or with the AWS CLI pointed at this emulator.')}
        />
      )}
      <div className="split">
        <Pick items={(buckets.data ?? []).map((b) => ({ id: b.name, label: b.name }))} selected={bucket} onSelect={setBucket} />
        {bucket && (
          <div className="detail">
            <Feedback error={objects.error} />
            {objects.data && objects.data.objects.length === 0 && (
              <EmptyState title={pick(lang, 'El bucket está vacío', 'The bucket is empty')} body={pick(lang, 'Los objetos que subas aparecerán aquí.', 'Objects you upload will show up here.')} />
            )}
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

function SqsSection({ t, lang }: Ctx) {
  const queues = usePolled(inspect.queues)
  const [queue, setQueue] = useState<string | null>(null)
  const messages = usePolled<QueueMessage[]>(() => (queue ? inspect.messages(queue).then((r) => r.messages) : Promise.resolve([])))
  useEffect(() => messages.reload(), [queue, messages.reload])

  return (
    <Section title="SQS · Amazon" cloud="aws" error={queues.error} down={queues.down} t={t}>
      {queues.data && queues.data.length === 0 && (
        <EmptyState title={pick(lang, 'Aún no hay colas', 'No queues yet')} body={pick(lang, 'Crea una cola desde tu aplicación y la verás aquí.', 'Create a queue from your app and it will show up here.')} />
      )}
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
            <Feedback error={messages.error} />
            {messages.data && messages.data.length === 0 && (
              <EmptyState title={pick(lang, 'La cola está vacía', 'The queue is empty')} body={pick(lang, 'Los mensajes que lleguen aparecerán aquí.', 'Messages that arrive will show up here.')} />
            )}
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

function DynamoSection({ t, lang }: Ctx) {
  const tables = usePolled(inspect.tables)
  const [table, setTable] = useState<string | null>(null)
  const items = usePolled(() => (table ? inspect.items(table) : Promise.resolve(null)))
  useEffect(() => items.reload(), [table, items.reload])

  return (
    <Section title="DynamoDB · Amazon" cloud="aws" error={tables.error} down={tables.down} t={t}>
      {tables.data && tables.data.length === 0 && (
        <EmptyState title={pick(lang, 'Aún no hay tablas', 'No tables yet')} body={pick(lang, 'Crea una tabla desde tu aplicación y la verás aquí.', 'Create a table from your app and it will show up here.')} />
      )}
      <div className="split">
        <Pick
          items={(tables.data ?? []).map((x) => ({ id: x.name, label: x.name, meta: `${x.items ?? '?'} · ${x.keys.join(', ')}` }))}
          selected={table}
          onSelect={setTable}
        />
        {table && (
          <div className="detail">
            <Feedback error={items.error} />
            {items.data && items.data.items.length === 0 && (
              <EmptyState title={pick(lang, 'La tabla está vacía', 'The table is empty')} body={pick(lang, 'Los elementos que guardes aparecerán aquí.', 'Items you store will show up here.')} />
            )}
            {items.data && items.data.items.length > 0 && <pre className="mini">{JSON.stringify(items.data.items, null, 2)}</pre>}
          </div>
        )}
      </div>
    </Section>
  )
}

function PubSubSection({ t, lang }: Ctx) {
  const [project, setProject] = useState('floci-local')
  const pubsub = usePolled<PubSub>(() => inspect.pubsub(project))
  useEffect(() => pubsub.reload(), [project, pubsub.reload])

  return (
    <Section title="Pub/Sub · Google Cloud" cloud="gcp" error={pubsub.error} down={pubsub.down} t={t}>
      <div className="reg-form insp-project">
        <Field label={t('project')}>{(a) => <input {...a} value={project} onChange={(e) => setProject(e.target.value.trim())} />}</Field>
      </div>
      {pubsub.data && pubsub.data.topics.length === 0 && pubsub.data.subscriptions.length === 0 && (
        <EmptyState
          title={pick(lang, 'Aún no hay temas ni suscripciones', 'No topics or subscriptions yet')}
          body={pick(lang, 'Crea un tema desde tu aplicación y lo verás aquí.', 'Create a topic from your app and it will show up here.')}
        />
      )}
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
