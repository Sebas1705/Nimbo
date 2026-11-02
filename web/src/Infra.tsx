import { useMemo } from 'react'
import { groupByCloud, type DockerStatus, type Provider, type Stack, type StackList } from './api'
import { extraPorts, primaryPort } from './guideLogic'
import { guideFor, stackBlurb } from './guides'
import type { Key, Lang } from './i18n'

type T = (k: Key) => string

/** What a card can ask for: the container lifecycle, the "keep my data" switch, and deleting what was kept. */
export type ProviderAction = 'start' | 'stop' | 'remove' | 'persistOn' | 'persistOff' | 'deleteData'

interface Props {
  t: T
  lang: Lang
  docker: DockerStatus | null
  providers: Provider[]
  stackList: StackList
  busy: Set<string>
  errors: Record<string, string>
  stackBusy: string | null
  stackErrors: Record<string, string>
  act: (p: Provider, action: ProviderAction) => void
  runStack: (s: Stack, action: 'stackUp' | 'stackDown') => void
  onPanel: (kind: 'logs' | 'env', p: Provider) => void
  onExport: (s: Stack) => void
  onGuide: (providerId: string) => void
}

/** Stacks (groups that start together) on top, then every provider as a module with its port on the front plate. */
export function InfraView(props: Props) {
  const { t, lang, docker, providers, stackList, stackBusy, stackErrors, runStack, onExport } = props
  const groups = useMemo(() => groupByCloud(providers), [providers])
  const names = new Map(providers.map((p) => [p.id, p.name]))

  return (
    <div className="infra">
      {stackList.stacks.length > 0 && (
        <section aria-label={t('stacks')}>
          <h2 className="eyebrow">{t('stacks')}</h2>
          <div className="presets">
            {stackList.stacks.map((s) => (
              <article key={s.id} className="preset" data-state={s.state}>
                <header>
                  <h3>{s.id}</h3>
                  <span className={`pill stack-${s.state}`}>
                    <i className={s.state === 'running' ? 'led on' : 'dot'} />
                    {t(`stack_${s.state}` as Key)} · {s.running}/{s.total}
                  </span>
                </header>
                <p>{stackBlurb(s.id, lang, s.description)}</p>
                <ul className="chips">
                  {s.providers.map((id) => (
                    <li key={id}>{names.get(id) ?? id}</li>
                  ))}
                </ul>
                {stackErrors[s.id] && <p className="err">{stackErrors[s.id]}</p>}
                <div className="actions">
                  <button className="primary" disabled={stackBusy !== null || !docker?.available || s.state === 'running'} onClick={() => runStack(s, 'stackUp')}>
                    {stackBusy === s.id ? '…' : t('up')}
                  </button>
                  <button className="ghost" disabled={stackBusy !== null || s.running === 0} onClick={() => runStack(s, 'stackDown')}>
                    {t('down')}
                  </button>
                  <button className="ghost" onClick={() => onExport(s)}>
                    {t('export')}
                  </button>
                </div>
              </article>
            ))}
          </div>
          {stackList.problems.length > 0 && (
            <div className="banner" role="alert">
              <b>{t('problems')}</b>
              <ul>
                {stackList.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </div>
          )}
        </section>
      )}

      {providers.length === 0 && <p className="muted">{t('loading')}</p>}

      {groups.map(([cloud, list]) => (
        <section key={cloud} className={`cloud cloud-${cloud}`}>
          <h2 className="eyebrow cloud-title">{t(`cloud_${cloud}` as Key)}</h2>
          <div className="modules">
            {list.map((p) => (
              <Module key={p.id} p={p} {...props} />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function Module({ p, t, lang, docker, busy, errors, act, onPanel, onGuide }: Props & { p: Provider }) {
  const port = primaryPort(p)
  const more = extraPorts(p)
  const blurb = guideFor(p.id)?.blurb[lang] ?? p.description

  return (
    <article className="module" data-state={p.state}>
      <header>
        <h3>{p.name}</h3>
        <span className={`pill ${p.state}`}>
          <i className={p.state === 'running' ? 'led on' : 'dot'} />
          {t(`state_${p.state}` as Key)}
        </span>
      </header>

      <div className="plate" aria-label={`${t('portLabel')} ${port ?? ''}`}>
        <span className="plate-port">{port ?? '—'}</span>
        <span className="plate-label">{more.length > 0 ? `${t('portLabel')} · +${more.length}` : t('portLabel')}</span>
      </div>

      <p className="blurb">{blurb}</p>
      <p className="image">
        <code>{p.image}</code>
      </p>

      {p.persistable && (
        <div className="keep">
          <label>
            <input type="checkbox" checked={p.persist === true} disabled={busy.has(p.id)} onChange={(e) => act(p, e.target.checked ? 'persistOn' : 'persistOff')} />
            <span>{lang === 'es' ? 'Conservar datos' : 'Keep data'}</span>
          </label>
          {p.persist && p.state === 'running' && (
            <small>{lang === 'es' ? 'Se aplica al arrancar de nuevo.' : 'Applies the next time it starts.'}</small>
          )}
          {p.hasData && (
            <button className="ghost danger small" disabled={busy.has(p.id) || p.state === 'running'} title={p.state === 'running' ? (lang === 'es' ? 'Párala antes' : 'Stop it first') : undefined} onClick={() => act(p, 'deleteData')}>
              {lang === 'es' ? 'Borrar datos guardados' : 'Delete saved data'}
            </button>
          )}
        </div>
      )}

      {errors[p.id] && <p className="err">{errors[p.id]}</p>}
      <div className="actions">
        {p.state === 'running' ? (
          <button disabled={busy.has(p.id)} onClick={() => act(p, 'stop')}>
            {t('stop')}
          </button>
        ) : (
          <button className="primary" disabled={busy.has(p.id) || !docker?.available} onClick={() => act(p, 'start')}>
            {t('start')}
          </button>
        )}
        <button className="ghost" onClick={() => onGuide(p.id)}>
          {t('guideBtn')}
        </button>
        <button className="ghost" disabled={p.state === 'not_created'} onClick={() => onPanel('logs', p)}>
          {t('logs')}
        </button>
        <button className="ghost" onClick={() => onPanel('env', p)}>
          {t('env')}
        </button>
        {p.state === 'stopped' && (
          <button className="ghost danger" disabled={busy.has(p.id)} onClick={() => act(p, 'remove')}>
            {t('remove')}
          </button>
        )}
      </div>
    </article>
  )
}
