import { useEffect, useState } from 'react'
import { labProgress, labs, system, type Lab, type LabList, type LabVerify } from './api'
import type { Key, Lang } from './i18n'
import './screens.css'
import { idsToImport } from './systemLogic'
import { Card, Feedback, Notice, Pill, useAction, useLoad } from './ui'

type T = (k: Key) => string

/** `focus` opens a lab straight away, for the links on the guide page. */
export function LabsView({ t, lang, focus = null }: { t: T; lang: Lang; focus?: string | null }) {
  const [selected, setSelected] = useState<string | null>(focus)
  const [done, setDone] = useState<Set<string>>(() => labProgress.load())
  const { data: list, error } = useLoad<LabList>(labs.list)

  // Progress lives in the data folder. What an older version kept in this browser is handed over once, then the browser
  // copy is only a cache for the first paint.
  useEffect(() => {
    system
      .progress()
      .then(async (server) => {
        const mine = labProgress.load()
        const toSend = idsToImport(mine, server.labs)
        const merged = toSend.length > 0 ? await system.importProgress(toSend) : server
        const completed = new Set(Object.entries(merged.labs).filter(([, r]) => r.completedAt != null).map(([id]) => id))
        const all = new Set([...mine, ...completed])
        labProgress.save(all)
        setDone(all)
      })
      .catch(() => {})
  }, [])

  const markDone = (id: string) =>
    setDone((prev) => {
      const next = new Set(prev).add(id)
      labProgress.save(next)
      return next
    })

  if (!list) return <Feedback working={error ? '' : t('loading')} error={error} />
  const lab = list.labs.find((l) => l.id === selected) ?? null

  return (
    <div className="labs">
      <div className="chaos-head">
        <span className="chip">
          <b>{list.labs.filter((l) => done.has(l.id)).length}</b>&nbsp;/ {list.labs.length} {t('done').toLowerCase()}
        </span>
      </div>

      {list.problems.length > 0 && (
        <Notice title={t('labsProblems')}>
          <ul>
            {list.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </Notice>
      )}

      <div className="lab-layout">
        <ul className="lab-list">
          {list.labs.map((l) => (
            <li key={l.id}>
              <button className={l.id === selected ? 'lab-card on' : 'lab-card'} onClick={() => setSelected(l.id)}>
                <span className="lab-level" aria-label={`${t('level')} ${l.level}`}>
                  {'●'.repeat(l.level)}
                  <span className="dim">{'●'.repeat(3 - l.level)}</span>
                </span>
                <span className="lab-title">{l.title[lang]}</span>
                {done.has(l.id) && <span className="lab-done">✓</span>}
                <small>{l.summary[lang]}</small>
              </button>
            </li>
          ))}
        </ul>

        {lab && <LabDetail key={lab.id} lab={lab} t={t} lang={lang} completed={done.has(lab.id)} onPassed={() => markDone(lab.id)} />}
      </div>
    </div>
  )
}

function LabDetail({ lab, t, lang, completed, onPassed }: { lab: Lab; t: T; lang: Lang; completed: boolean; onPassed: () => void }) {
  const [result, setResult] = useState<LabVerify | null>(null)
  const action = useAction()

  const verify = () =>
    action.run('', async () => {
      const r = await labs.verify(lab.id)
      setResult(r)
      if (r.passed) onPassed()
    })

  return (
    <Card title={lab.title[lang]} actions={completed ? <Pill tone="ok">{t('done')}</Pill> : undefined}>
      <p className="desc">{lab.summary[lang]}</p>
      {lab.stack && (
        <p className="hint">
          {t('suggestedStack')}: <code>{lab.stack}</code>
        </p>
      )}

      <h4>{t('labSteps')}</h4>
      <ol className="lab-steps">
        {lab.steps.map((s, i) => (
          <li key={i}>{s[lang]}</li>
        ))}
      </ol>

      {lab.hints.length > 0 && (
        <details>
          <summary>{t('labHints')}</summary>
          <ul>
            {lab.hints.map((h, i) => (
              <li key={i}>{h[lang]}</li>
            ))}
          </ul>
        </details>
      )}
      {lab.solution.length > 0 && (
        <details>
          <summary>{t('labSolution')}</summary>
          {lab.solution.map((s, i) => (
            <div key={i}>
              {s.title && <p className="hint">{s.title[lang]}</p>}
              <pre className="mini">{s.code}</pre>
            </div>
          ))}
        </details>
      )}

      <h4>{t('labChecks')}</h4>
      <ul className="lab-checks">
        {lab.checks.map((c, i) => {
          const r = result?.results.find((x) => x.index === i)
          return (
            <li key={i} className={r ? (r.ok ? 'ok' : 'bad') : ''}>
              <span className="mark">{r ? (r.ok ? '✓' : '✗') : '○'}</span>
              <span>{c.label[lang]}</span>
              {r && !r.ok && r.detail && <small>{r.detail}</small>}
            </li>
          )
        })}
      </ul>

      <Feedback error={action.error} />
      <div className="actions">
        <button className="primary" disabled={action.busy} onClick={verify}>
          {action.busy ? '…' : t('verify')}
        </button>
        {result && <span className={result.passed ? 'lab-ok' : 'muted'}>{result.passed ? t('labPassed') : t('labPending')}</span>}
      </div>
    </Card>
  )
}
