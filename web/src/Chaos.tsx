import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, chaos, chaosState, type ChaosPreset, type ChaosTarget, type ChaosView } from './api'
import type { Key } from './i18n'

type T = (k: Key) => string

export function ChaosView({ t }: { t: T }) {
  const [view, setView] = useState<ChaosView | null>(null)
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)

  const refresh = useCallback(() => {
    chaos
      .view()
      .then((v) => {
        setView(v)
        setError('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, 3000)
    return () => clearInterval(id)
  }, [refresh])

  const startToxiproxy = async () => {
    setStarting(true)
    try {
      await api.start('toxiproxy')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(false)
      setTimeout(refresh, 1500)
    }
  }

  const replace = (updated: ChaosTarget) =>
    setView((v) => (v ? { ...v, targets: v.targets.map((x) => (x.id === updated.id ? updated : x)) } : v))

  const resetAll = async () => {
    try {
      setView(await chaos.reset())
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  if (!view) return <p className="muted">{error || '…'}</p>

  return (
    <div className="chaos">
      <div className="chaos-head">
        {view.available && (
          <button className="ghost" onClick={resetAll}>
            {t('resetAll')}
          </button>
        )}
      </div>

      {!view.available && (
        <div className="banner" role="alert">
          {t('chaosDown')}{' '}
          <button className="primary" disabled={starting} onClick={startToxiproxy}>
            {starting ? '…' : t('startToxiproxy')}
          </button>
        </div>
      )}
      {error && <p className="err">{error}</p>}

      <div className="grid">
        {view.targets.map((target) => (
          <TargetCard key={target.id} target={target} presets={view.presets} disabled={!view.available} t={t} onChange={replace} />
        ))}
      </div>
    </div>
  )
}

function TargetCard({
  target,
  presets,
  disabled,
  t,
  onChange,
}: {
  target: ChaosTarget
  presets: ChaosPreset[]
  disabled: boolean
  t: T
  onChange: (t: ChaosTarget) => void
}) {
  const [presetId, setPresetId] = useState('latency')
  const [values, setValues] = useState<Record<string, number>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const preset = presets.find((p) => p.id === presetId) ?? presets[0]
  const state = chaosState(target)

  const run = async (fn: () => Promise<ChaosTarget>) => {
    setBusy(true)
    setError('')
    try {
      onChange(await fn())
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const apply = () => {
    const params = Object.fromEntries(preset.params.map((p) => [p.key, values[`${preset.id}.${p.key}`] ?? p.default]))
    return run(() => chaos.apply(target.id, preset.id, params))
  }

  return (
    <article className="card chaos-card" data-chaos={state}>
      <div className="card-head">
        <h3>{target.name}</h3>
        <span className={`pill chaos-${state}`}>
          <i className="dot" />
          {t(`chaos_${state}` as Key)}
        </span>
      </div>
      <div className="meta">
        <code>{target.endpoint}</code>
        <code>→ :{target.upstreamPort}</code>
      </div>

      {target.toxics.length > 0 && (
        <div className="toxics">
          {target.toxics.map((x) => (
            <span key={x.name} className="toxic">
              {t(`preset_${x.name}` as Key)}
              {Object.entries(x.attributes).length > 0 && <small> {Object.values(x.attributes).join('/')}</small>}
              {x.toxicity < 1 && <small> {Math.round(x.toxicity * 100)}%</small>}
              <button aria-label="remove" disabled={busy} onClick={() => run(() => chaos.removeToxic(target.id, x.name))}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}

      <select value={presetId} onChange={(e) => setPresetId(e.target.value)} disabled={disabled} aria-label="preset">
        {presets.map((p) => (
          <option key={p.id} value={p.id}>
            {t(`preset_${p.id}` as Key)}
          </option>
        ))}
      </select>
      <p className="desc">{t(`desc_${preset.id}` as Key)}</p>
      <div className="params">
        {preset.params.map((p) => (
          <label key={p.key} className="field">
            {t(`param_${p.key}` as Key)} ({p.unit})
            <input
              type="number"
              min={p.min}
              max={p.max}
              value={values[`${preset.id}.${p.key}`] ?? p.default}
              onChange={(e) => setValues((v) => ({ ...v, [`${preset.id}.${p.key}`]: Number(e.target.value) }))}
            />
          </label>
        ))}
      </div>

      {error && <p className="err">{error}</p>}
      <div className="actions">
        <button className="primary" disabled={disabled || busy} onClick={apply}>
          {t('apply')}
        </button>
        <button className="ghost" disabled={disabled || busy || !target.active} onClick={() => run(() => chaos.clear(target.id))}>
          {t('clearFaults')}
        </button>
      </div>
    </article>
  )
}
