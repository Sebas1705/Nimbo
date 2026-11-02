import { useState } from 'react'
import { coverage, fidelity, type FitRef, type FitState, type FitSuite } from './api'
import type { Key } from './i18n'
import './screens.css'
import { Feedback, Pill, useAction, useLoad, type Tone } from './ui'

type T = (k: Key) => string

const CLOUDS: Array<{ id: string; name: string }> = [
  { id: 'aws', name: 'Amazon Web Services' },
  { id: 'azure', name: 'Microsoft Azure' },
  { id: 'gcp', name: 'Google Cloud' },
]

const fitTone = (s: FitSuite['status'] | 'offline'): Tone => (s === 'full' ? 'ok' : s === 'partial' ? 'warn' : s === 'missing' ? 'bad' : 'idle')

const MARK = { pass: '✓', fail: '✗', skipped: '–' } as const

export function FidelityView({ t }: { t: T }) {
  const loaded = useLoad(fidelity.state)
  // A run answers with the new state, which is shown at once instead of waiting for another read.
  const [ran, setRan] = useState<FitState | null>(null)
  const [scope, setScope] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const action = useAction()
  const state = ran ?? loaded.data

  const run = (cloud?: string) => {
    setScope(cloud ?? 'all')
    return action.run('', async () => {
      setRan(await fidelity.run(cloud))
    })
  }
  const busy = action.busy ? scope : null

  if (!state) return <Feedback working={loaded.error ? '' : t('loading')} error={loaded.error} />

  const byId = new Map(state.results.map((r) => [r.id, r]))
  const pct = coverage(state.results)

  return (
    <div className="fidelity">
      <div className="chaos-head">
        <div className="actions">
          <button className="primary" disabled={action.busy} onClick={() => run()}>
            {busy === 'all' ? '…' : t('runAllChecks')}
          </button>
        </div>
      </div>

      <div className="fit-summary">
        {(['full', 'partial', 'missing', 'offline'] as const).map((s) => (
          <Pill key={s} tone={fitTone(s)}>
            {t(`fit_${s}` as Key)} · {state.summary[s] ?? 0}
          </Pill>
        ))}
        {state.results.length > 0 && (
          <span className="chip">
            <b>{pct}%</b>&nbsp;{t('coverage')}
          </span>
        )}
        <span className="muted small">{state.updatedAt ? `${t('lastRun')}: ${new Date(state.updatedAt).toLocaleTimeString()}` : t('neverRun')}</span>
      </div>

      <Feedback error={action.error || loaded.error} />

      {CLOUDS.map((cloud) => {
        const suites = state.catalog.filter((c) => c.cloud === cloud.id)
        if (suites.length === 0) return null
        return (
          <section key={cloud.id} className={`cloud cloud-${cloud.id}`}>
            <h2>{cloud.name}</h2>
            <div className="fit-actions">
              <button className="ghost" disabled={action.busy} onClick={() => run(cloud.id)}>
                {busy === cloud.id ? '…' : `${t('runAllChecks')} · ${cloud.id.toUpperCase()}`}
              </button>
            </div>
            <ul className="fit-list">
              {suites.map((ref) => (
                <SuiteRow key={ref.id} refItem={ref} result={byId.get(ref.id)} open={open === ref.id} onToggle={() => setOpen(open === ref.id ? null : ref.id)} t={t} />
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

function SuiteRow({
  refItem,
  result,
  open,
  onToggle,
  t,
}: {
  refItem: FitRef
  result?: FitSuite
  open: boolean
  onToggle: () => void
  t: T
}) {
  const passed = result?.steps.filter((s) => s.status === 'pass').length ?? 0
  return (
    <li className="fit-row" data-status={result?.status ?? 'none'}>
      <button className="fit-head" onClick={onToggle} disabled={!result} aria-expanded={open}>
        <span className="fit-name">{refItem.name}</span>
        {result ? (
          <>
            <Pill tone={fitTone(result.status)}>{t(`fit_${result.status}` as Key)}</Pill>
            <small>
              {passed}/{result.steps.length} {t('stepsOk')} · {result.ms} ms
            </small>
          </>
        ) : (
          <small className="muted">{t('neverRun')}</small>
        )}
      </button>
      {open && result && (
        <ol className="fit-steps">
          {result.steps.map((s) => (
            <li key={s.name} className={`step-${s.status}`}>
              <span className="mark">{MARK[s.status]}</span>
              <span>{s.name}</span>
              {s.detail && <small>{s.detail}</small>}
            </li>
          ))}
        </ol>
      )}
    </li>
  )
}
