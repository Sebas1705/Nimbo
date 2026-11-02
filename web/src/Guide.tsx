import type { DockerStatus, Provider } from './api'
import { RECIPES, type Recipe } from './guides'
import { primaryPort, recipeStatus, runningCount, stepStatus } from './guideLogic'
import type { Key, Lang } from './i18n'
import type { TabId } from './nav'
import { Card, Feedback, Pill, useAction } from './ui'

type T = (k: Key) => string

interface Props {
  t: T
  lang: Lang
  docker: DockerStatus | null
  providers: Provider[]
  /** Starts the given providers in order; resolves to an error message, or null when all went well. */
  startMany: (ids: string[]) => Promise<string | null>
  stop: (ids: string[]) => Promise<void>
  go: (tab: TabId) => void
  openLab: (id: string) => void
  openGuide: (providerId: string) => void
}

/** The page you land on: what is on right now, the five steps, and one-click recipes to bring things up. */
export function GuideView(props: Props) {
  const { t, docker, providers, go, openGuide } = props
  const running = runningCount(providers)
  const steps = stepStatus(docker, providers)

  const stepRows: Array<{ title: Key; body: string; to?: { tab: TabId; label: Key } }> = [
    { title: 'step1', body: steps.done[0] ? t('step1Done') : t('step1Todo') },
    { title: 'step2', body: running > 0 ? `${running} ${t('running')}` : t('step2Body') },
    { title: 'step3', body: t('step3Body'), to: { tab: 'infra', label: 'viewInfra' } },
    { title: 'step4', body: t('step4Body'), to: { tab: 'inspector', label: 'viewInspector' } },
    { title: 'step5', body: t('step5Body'), to: { tab: 'chaos', label: 'viewChaos' } },
  ]

  return (
    <div className="guide">
      <section aria-label={t('statusTitle')}>
        <h2 className="eyebrow">{t('statusTitle')}</h2>
        <ul className="rack">
          {providers.map((p) => (
            <li key={p.id}>
              <button className="tile" data-state={p.state} onClick={() => openGuide(p.id)} title={p.name}>
                <i className={p.state === 'running' ? 'led on' : 'led'} />
                <span className="tile-name">{p.name}</span>
                <span className="tile-port">{primaryPort(p) ?? ''}</span>
              </button>
            </li>
          ))}
        </ul>
        {providers.length > 0 && running === 0 && <p className="muted">{t('statusNothing')}</p>}
      </section>

      <section aria-label={t('stepsTitle')}>
        <h2 className="eyebrow">{t('stepsTitle')}</h2>
        <ol className="steps">
          {stepRows.map((s, i) => (
            <li key={s.title} className={steps.done[i] ? 'done' : ''} aria-current={steps.current === i ? 'step' : undefined}>
              <span className="step-mark" aria-hidden="true">
                {steps.done[i] ? '✓' : i + 1}
              </span>
              <div>
                <h3>{t(s.title)}</h3>
                <p>{s.body}</p>
                {s.to && (
                  <button className="link" onClick={() => go(s.to!.tab)}>
                    {t(s.to.label)} →
                  </button>
                )}
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section aria-label={t('recipesTitle')}>
        <h2 className="eyebrow">{t('recipesTitle')}</h2>
        <p className="muted">{t('recipesIntro')}</p>
        <div className="recipes">
          {RECIPES.map((r) => (
            <RecipeCard key={r.id} recipe={r} {...props} />
          ))}
        </div>
      </section>
    </div>
  )
}

function RecipeCard({ recipe, t, lang, docker, providers, startMany, stop, go, openLab }: Props & { recipe: Recipe }) {
  const status = recipeStatus(recipe, providers)
  const action = useAction()
  const byId = new Map(providers.map((p) => [p.id, p]))

  const start = () =>
    action.run('', async () => {
      const failure = await startMany(status.missing)
      if (failure) throw new Error(failure)
    })

  return (
    <Card
      title={recipe.title[lang]}
      tone={status.state === 'up' ? 'highlight' : undefined}
      actions={
        status.state === 'up' ? (
          <Pill tone="ok">{t('recipeUp')}</Pill>
        ) : status.state === 'partial' ? (
          <Pill tone="warn">
            {status.running}/{status.total}
          </Pill>
        ) : undefined
      }
    >
      <p className="recipe-summary">{recipe.summary[lang]}</p>

      <ul className="recipe-parts">
        {recipe.providers.map((id) => {
          const p = byId.get(id)
          return (
            <li key={id}>
              <i className={p?.state === 'running' ? 'led on' : 'led'} />
              {p?.name ?? id}
              {p && primaryPort(p) !== null && <code>{primaryPort(p)}</code>}
            </li>
          )
        })}
      </ul>

      <h4>{t('recipeYouGet')}</h4>
      <ul className="recipe-gets">
        {recipe.youGet.map((g, i) => (
          <li key={i}>{g[lang]}</li>
        ))}
      </ul>

      <Feedback error={action.error} />
      <div className="actions">
        {status.state === 'up' ? (
          <button onClick={() => stop(recipe.providers)}>{t('recipeStop')}</button>
        ) : (
          <button className="primary" disabled={action.busy || !docker?.available} onClick={start}>
            {action.busy ? t('recipeStarting') : t('recipeStart')}
          </button>
        )}
        <button className={status.state === 'up' ? 'link strong' : 'link'} onClick={() => go(recipe.next.tab)}>
          {recipe.next.label[lang]} →
        </button>
        {recipe.lab && (
          <button className="link" onClick={() => openLab(recipe.lab!)}>
            {t('recipeLab')} →
          </button>
        )}
      </div>
    </Card>
  )
}
