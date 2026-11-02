import { useEffect, useState } from 'react'
import { ApiError, barStyle, formatMicros, traces, type TraceDetail, type TraceSummary } from './api'
import type { Key, Lang } from './i18n'
import './screens.css'
import { EmptyState, Feedback, Notice, pick, useAction, useLoad } from './ui'

type T = (k: Key) => string

/** The services Jaeger knows, or the fact that Jaeger is not there (503). */
async function loadServices(): Promise<{ down: boolean; services: string[] }> {
  try {
    return { down: false, services: await traces.services() }
  } catch (e) {
    if (e instanceof ApiError && e.status === 503) return { down: true, services: [] }
    throw e
  }
}

export function TracesView({ t, lang }: { t: T; lang: Lang }) {
  const known = useLoad(loadServices, 5000)
  const [service, setService] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const send = useAction()

  const services = known.data?.services ?? null
  const down = known.data?.down === true

  useEffect(() => {
    if (!services) return
    setService((cur) => (cur && services.includes(cur) ? cur : (services.find((x) => x !== 'jaeger-all-in-one') ?? services[0] ?? '')))
  }, [services])

  // Each answer says which service it is for, so a slow answer for the one you just left is not shown.
  const traceList = useLoad(async () => ({ service, list: service ? await traces.list(service) : ([] as TraceSummary[]) }), 4000)
  const reloadList = traceList.reload
  useEffect(() => reloadList(), [service, reloadList])
  const list: TraceSummary[] = traceList.data?.service === service ? traceList.data.list : []

  const traceDetail = useLoad(async () => (selected ? await traces.detail(selected) : null))
  const reloadDetail = traceDetail.reload
  useEffect(() => reloadDetail(), [selected, reloadDetail])
  const detail: TraceDetail | null = selected && traceDetail.data?.traceId === selected ? traceDetail.data : null

  const sendSample = (failing: boolean) =>
    send.run('', async () => {
      const r = await traces.sample(failing)
      setService(r.service)
      setTimeout(known.reload, 1500)
    })

  if (down) {
    return (
      <div className="traces">
        <Notice>{t('jaegerDown')}</Notice>
      </div>
    )
  }
  if (!known.data) return <Feedback working={known.error ? '' : t('loading')} error={known.error} />

  return (
    <div className="traces">
      <div className="chaos-head">
        <div className="actions">
          <label className="field">
            {t('traceService')}
            <select
              value={service}
              onChange={(e) => {
                setService(e.target.value)
                setSelected(null)
              }}
              aria-label={t('traceService')}
            >
              {(services ?? []).map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <button className="primary" disabled={send.busy} onClick={() => sendSample(false)}>
            {t('sendSample')}
          </button>
          <button className="ghost" disabled={send.busy} onClick={() => sendSample(true)}>
            {t('sendFailing')}
          </button>
        </div>
      </div>
      <Feedback error={send.error || known.error || traceList.error || traceDetail.error} />
      {services && services.length === 0 && (
        <EmptyState title={pick(lang, 'Aún no hay trazas', 'No traces yet')} body={t('noServices')} action={t('sendSample')} onAction={() => sendSample(false)} />
      )}
      {service && list.length === 0 && services && services.length > 0 && traceList.data?.service === service && (
        <EmptyState title={pick(lang, 'Este servicio no tiene trazas', 'This service has no traces')} body={t('noTraces')} />
      )}

      <div className="split trace-split">
        <ul className="pick">
          {list.map((tr) => (
            <li key={tr.traceId}>
              <button className={tr.traceId === selected ? 'row on' : 'row'} onClick={() => setSelected(tr.traceId)}>
                <span>
                  {tr.operation}
                  {tr.errors > 0 && <span className="trace-err"> ● {tr.errors}</span>}
                </span>
                <small>
                  {formatMicros(tr.durationMicros)} · {tr.spans} {t('spansLabel')} · {new Date(tr.startedAt).toLocaleTimeString()}
                </small>
              </button>
            </li>
          ))}
        </ul>

        <div className="detail">
          {!selected && list.length > 0 && <EmptyState title={pick(lang, 'Ninguna traza elegida', 'No trace picked')} body={t('selectTrace')} />}
          {detail && <Waterfall detail={detail} />}
        </div>
      </div>
    </div>
  )
}

function Waterfall({ detail }: { detail: TraceDetail }) {
  return (
    <div className="waterfall" aria-label="waterfall">
      <div className="wf-head">
        <code>{detail.traceId}</code>
        <b>{formatMicros(detail.durationMicros)}</b>
      </div>
      {detail.spans.map((s) => {
        const { left, width } = barStyle(s, detail.durationMicros)
        return (
          <div key={s.id} className="wf-row">
            <span className="wf-label" style={{ paddingLeft: `${s.depth * 14}px` }} title={`${s.service} · ${s.operation}`}>
              {s.operation} <small>{s.service}</small>
            </span>
            <span className="wf-track">
              <span className={s.error ? 'wf-bar err' : 'wf-bar'} style={{ left: `${left}%`, width: `${width}%` }} />
            </span>
            <small className="wf-time">{formatMicros(s.durationMicros)}</small>
          </div>
        )
      })}
    </div>
  )
}
