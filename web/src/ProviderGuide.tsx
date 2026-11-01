import { CodeBlock } from './CodeBlock'
import type { Provider } from './api'
import type { ProviderGuide } from './guides'
import type { Key, Lang } from './i18n'
import type { TabId } from './nav'

type T = (k: Key) => string

interface Props {
  provider: Provider
  guide: ProviderGuide | undefined
  t: T
  lang: Lang
  starting: boolean
  dockerUp: boolean
  onStart: () => void
  onClose: () => void
  go: (tab: TabId) => void
}

/** Everything someone needs to use one provider: what it is, where it listens, commands to paste, and the traps. */
export function ProviderGuidePanel({ provider, guide, t, lang, starting, dockerUp, onStart, onClose, go }: Props) {
  const running = provider.state === 'running'

  return (
    <aside className="panel guide-panel" role="dialog" aria-label={provider.name}>
      <div className="panel-head">
        <h3>
          <i className={running ? 'led on' : 'led'} /> {provider.name}
        </h3>
        <button className="ghost" onClick={onClose}>
          {t('close')}
        </button>
      </div>

      <div className="guide-body">
        <div className="guide-state">
          <span className={`pill ${provider.state}`}>
            <i className="dot" />
            {t(`state_${provider.state}` as Key)}
          </span>
          {!running && (
            <button className="primary" disabled={starting || !dockerUp} onClick={onStart}>
              {starting ? t('guideStarting') : t('guideStartIt')}
            </button>
          )}
        </div>

        <section>
          <h4>{t('guideWhat')}</h4>
          <p>{guide?.what[lang] ?? provider.description}</p>
        </section>

        {guide && (
          <section>
            <h4>{t('guideUseFor')}</h4>
            <p>{guide.useFor[lang]}</p>
          </section>
        )}

        <section>
          <h4>{t('guideWhere')}</h4>
          {guide && guide.endpoints.length > 0 ? (
            <dl className="endpoints">
              {guide.endpoints.map((e, i) => (
                <div key={i}>
                  <dt>{typeof e.label === 'string' ? e.label : e.label[lang]}</dt>
                  <dd>
                    <code>{e.value}</code>
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="mono-line">
              {provider.ports.map((p) => (
                <code key={p}>:{p}</code>
              ))}
            </p>
          )}
        </section>

        <section>
          <h4>{t('guideTry')}</h4>
          {guide && guide.snippets.length > 0 ? (
            guide.snippets.map((s, i) => <CodeBlock key={i} title={s.title[lang]} code={s.code} t={t} />)
          ) : (
            <p className="muted">{t('guideNoSnippets')}</p>
          )}
        </section>

        {guide && guide.watchOut.length > 0 && (
          <section>
            <h4>{t('guideWatch')}</h4>
            <ul className="watch">
              {guide.watchOut.map((w, i) => (
                <li key={i}>{w[lang]}</li>
              ))}
            </ul>
          </section>
        )}

        {guide?.next && (
          <button className="link strong" onClick={() => go(guide.next!.tab)}>
            {t('guideNext')}: {guide.next.label[lang]} →
          </button>
        )}
      </div>
    </aside>
  )
}
