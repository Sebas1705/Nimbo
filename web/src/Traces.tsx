import { useCallback, useEffect, useState } from 'react'
import { ApiError, barStyle, formatMicros, traces, type TraceDetail, type TraceSummary } from './api'
import type { Key } from './i18n'

type T = (k: Key) => string

export function TracesView({ t }: { t: T }) {
  const [services, setServices] = useState<string[] | null>(null)
  const [down, setDown] = useState(false)
  const [service, setService] = useState('')
  const [list, setList] = useState<TraceSummary[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<TraceDetail | null>(null)
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    traces
      .services()
      .then((s) => {
        setServices(s)
        setDown(false)
        setService((cur) => (cur && s.includes(cur) ? cur : (s.find((x) => x !== 'jaeger-all-in-one') ?? s[0] ?? '')))
      })
      .catch((e) => {
        setDown(e instanceof ApiError && e.status === 503)
        setError(e instanceof Error ? e.message : String(e))
      })
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, 5000)
    return () => clearInterval(id)
  }, [refresh])

  useEffect(() => {
    if (!service) {
      setList([])
      return
    }
    let alive = true
    const load = () =>
      traces
        .list(service)
        .then((l) => alive && setList(l))
        .catch((e) => alive && setError(e instanceof Error ? e.message : String(e)))
    load()
    const id = setInterval(load, 4000)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [service])

  useEffect(() => {
    if (!selected) {
      setDetail(null)
      return
    }
    let alive = true
    traces
      .detail(selected)
      .then((d) => alive && setDetail(d))
      .catch((e) => alive && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      alive = false
    }
  }, [selected])

  const send = async (failing: boolean) => {
    setError('')
    try {
      const r = await traces.sample(failing)
      setService(r.service)
      setTimeout(refresh, 1500)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  if (down) {
    return (
      <div className="traces">
        <div className="banner" role="alert">
          {t('jaegerDown')}
        </div>
      </div>
    )
  }

  return (
    <div className="traces">
      <div className="chaos-head">
        <div className="actions">
          <label className="field">
            {t('traceService')}
            <select value={service} onChange={(e) => { setService(e.target.value); setSelected(null) }} aria-label={t('traceService')}>
              {(services ?? []).map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <button className="primary" onClick={() => send(false)}>
            {t('sendSample')}
          </button>
          <button className="ghost" onClick={() => send(true)}>
            {t('sendFailing')}
          </button>
        </div>
      </div>
      {error && <p className="err">{error}</p>}
      {services && services.length === 0 && <p className="muted">{t('noServices')}</p>}
      {service && list.length === 0 && services && services.length > 0 && <p className="muted">{t('noTraces')}</p>}

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
          {!detail && <p className="muted">{t('selectTrace')}</p>}
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
