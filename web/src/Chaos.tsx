import { useState } from 'react'
import { api, chaos, chaosState, type ChaosPreset, type ChaosTarget } from './api'
import type { Key, Lang } from './i18n'
import './screens.css'
import { Card, EmptyState, Feedback, Field, Notice, Pill, pick, useAction, useLoad, type Tone } from './ui'

type T = (k: Key) => string

const stateTone = (s: ReturnType<typeof chaosState>): Tone => (s === 'healthy' ? 'ok' : s === 'faults' ? 'warn' : 'bad')

export function ChaosView({ t, lang }: { t: T; lang: Lang }) {
  const { data: view, error: loadError, reload } = useLoad(chaos.view, 3000)
  const toxiproxy = useAction()
  const reset = useAction()

  const startToxiproxy = () =>
    toxiproxy.run('', async () => {
      try {
        await api.start('toxiproxy')
      } finally {
        setTimeout(reload, 1500)
      }
    })

  const resetAll = () =>
    reset.run('', async () => {
      await chaos.reset()
      reload()
    })

  if (!view) return <Feedback working={loadError ? '' : t('loading')} error={loadError} />

  return (
    <div className="chaos">
      <div className="chaos-head">
        {view.available && (
          <button className="ghost" disabled={reset.busy} onClick={resetAll}>
            {t('resetAll')}
          </button>
        )}
      </div>

      {!view.available && (
        <Notice>
          <span className="notice-line">
            {t('chaosDown')}
            <button className="primary" disabled={toxiproxy.busy} onClick={startToxiproxy}>
              {toxiproxy.busy ? '…' : t('startToxiproxy')}
            </button>
          </span>
        </Notice>
      )}
      <Feedback error={toxiproxy.error || reset.error || loadError} />

      {view.targets.length === 0 && <EmptyState title={pick(lang, 'Sin destinos', 'No targets')} body={pick(lang, 'No hay nada a lo que inyectar fallos todavía.', 'There is nothing to inject faults into yet.')} />}
      <div className="grid">
        {view.targets.map((target) => (
          <TargetCard key={target.id} target={target} presets={view.presets} disabled={!view.available} t={t} lang={lang} onChanged={reload} />
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
  lang,
  onChanged,
}: {
  target: ChaosTarget
  presets: ChaosPreset[]
  disabled: boolean
  t: T
  lang: Lang
  onChanged: () => void
}) {
  const [presetId, setPresetId] = useState('latency')
  const [values, setValues] = useState<Record<string, number>>({})
  const action = useAction()
  const preset = presets.find((p) => p.id === presetId) ?? presets[0]
  const state = chaosState(target)

  const run = (fn: () => Promise<unknown>) =>
    action.run('', async () => {
      await fn()
      onChanged()
    })

  const valueOf = (p: ChaosPreset['params'][number]) => values[`${preset.id}.${p.key}`] ?? p.default
  const rangeError = (p: ChaosPreset['params'][number]) => {
    const v = valueOf(p)
    return Number.isFinite(v) && v >= p.min && v <= p.max ? '' : pick(lang, `Entre ${p.min} y ${p.max}.`, `Between ${p.min} and ${p.max}.`)
  }
  const invalid = preset.params.some((p) => rangeError(p) !== '')

  const apply = () => {
    const params = Object.fromEntries(preset.params.map((p) => [p.key, valueOf(p)]))
    return run(() => chaos.apply(target.id, preset.id, params))
  }

  return (
    <div className="chaos-wrap" data-chaos={state}>
      <Card title={target.name} actions={<Pill tone={stateTone(state)}>{t(`chaos_${state}` as Key)}</Pill>}>
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
                <button aria-label={pick(lang, `Quitar ${t(`preset_${x.name}` as Key)}`, `Remove ${t(`preset_${x.name}` as Key)}`)} disabled={action.busy} onClick={() => run(() => chaos.removeToxic(target.id, x.name))}>
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
        <div className="reg-form params">
          {preset.params.map((p) => (
            <Field key={p.key} label={`${t(`param_${p.key}` as Key)} (${p.unit})`} error={rangeError(p)}>
              {(a) => (
                <input
                  {...a}
                  type="number"
                  min={p.min}
                  max={p.max}
                  value={valueOf(p)}
                  onChange={(e) => setValues((v) => ({ ...v, [`${preset.id}.${p.key}`]: Number(e.target.value) }))}
                />
              )}
            </Field>
          ))}
        </div>

        <Feedback error={action.error} />
        <div className="actions">
          <button className="primary" disabled={disabled || action.busy || invalid} onClick={apply}>
            {t('apply')}
          </button>
          <button className="ghost" disabled={disabled || action.busy || !target.active} onClick={() => run(() => chaos.clear(target.id))}>
            {t('clearFaults')}
          </button>
        </div>
      </Card>
    </div>
  )
}
