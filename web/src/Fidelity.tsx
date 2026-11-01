import { useCallback, useEffect, useState } from 'react'
import { coverage, fidelity, type FitRef, type FitState, type FitSuite } from './api'
import type { Key } from './i18n'

type T = (k: Key) => string

const CLOUDS: Array<{ id: string; name: string }> = [
  { id: 'aws', name: 'Amazon Web Services' },
  { id: 'azure', name: 'Microsoft Azure' },
  { id: 'gcp', name: 'Google Cloud' },
]

const MARK = { pass: '✓', fail: '✗', skipped: '–' } as const

export function FidelityView({ t }: { t: T }) {
  const [state, setState] = useState<FitState | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [open, setOpen] = useState<string | null>(null)

  const load = useCallback(() => {
    fidelity
      .state()
      .then(setState)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  useEffect(load, [load])

  const run = async (cloud?: string) => {
    setBusy(cloud ?? 'all')
    setError('')
    try {
      setState(await fidelity.run(cloud))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  if (!state) return <p className="muted">{error || '…'}</p>

  const byId = new Map(state.results.map((r) => [r.id, r]))
  const pct = coverage(state.results)

  return (
    <div className="fidelity">
      <div className="chaos-head">
        <div className="actions">
          <button className="primary" disabled={busy !== null} onClick={() => run()}>
            {busy === 'all' ? '…' : t('runAllChecks')}
          </button>
        </div>
      </div>

      <div className="fit-summary">
        {(['full', 'partial', 'missing', 'offline'] as const).map((s) => (
          <span key={s} className={`pill fit-${s}`}>
            <i className="dot" />
            {t(`fit_${s}` as Key)} · {state.summary[s] ?? 0}
          </span>
        ))}
        {state.results.length > 0 && (
          <span className="chip">
            <b>{pct}%</b>&nbsp;{t('coverage')}
          </span>
        )}
        <span className="muted small">
          {state.updatedAt ? `${t('lastRun')}: ${new Date(state.updatedAt).toLocaleTimeString()}` : t('neverRun')}
        </span>
      </div>

      {error && <p className="err">{error}</p>}

      {CLOUDS.map((cloud) => {
        const suites = state.catalog.filter((c) => c.cloud === cloud.id)
        if (suites.length === 0) return null
        return (
          <section key={cloud.id} className={`cloud cloud-${cloud.id}`}>
            <h2>{cloud.name}</h2>
            <div className="fit-actions">
              <button className="ghost" disabled={busy !== null} onClick={() => run(cloud.id)}>
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
            <span className={`pill fit-${result.status}`}>
              <i className="dot" />
              {t(`fit_${result.status}` as Key)}
            </span>
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
