import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, countRunning, failures, system, updates as updatesApi, variables, type DockerStatus, type Provider, type Settings, type Stack, type StackList, type UpdateStatus } from './api'
import { Logo, TokenGate } from './Brand'
import { ChaosView } from './Chaos'
import { FidelityView } from './Fidelity'
import { GuideView } from './Guide'
import { HelpBox } from './Help'
import { InfraView, type ProviderAction } from './Infra'
import { InspectorView } from './Inspector'
import { KubeView } from './Kube'
import { LabsView } from './Labs'
import { ExportPanel, SidePanel, type Panel } from './Panels'
import { ProviderGuidePanel } from './ProviderGuide'
import { TracesView } from './Traces'
import { guideFor } from './guides'
import { detectLang, translate, type Key, type Lang } from './i18n'
import { NAV, groupOf, itemOf, startTab, type TabId } from './nav'
import { applyTheme, loadTheme, nextTheme, readPref, writePref, type Theme } from './prefs'
import { ActivityView, OfflineView, SettingsView, UpdatesView } from './System'
import { updateDot } from './systemLogic'

function initialLang(): Lang {
  const saved = readPref('lang')
  return saved === 'es' || saved === 'en' ? saved : detectLang()
}

export function App() {
  const [lang, setLang] = useState<Lang>(initialLang)
  const t = useCallback((k: Key) => translate(lang, k), [lang])
  const [theme, setTheme] = useState<Theme>(loadTheme)

  const [needsToken, setNeedsToken] = useState(false)
  const [docker, setDocker] = useState<DockerStatus | null>(null)
  const [providers, setProviders] = useState<Provider[]>([])
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState<Set<string>>(new Set())
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [panel, setPanel] = useState<Panel>(null)
  const [stackList, setStackList] = useState<StackList>({ stacks: [], problems: [] })
  const [stackBusy, setStackBusy] = useState<string | null>(null)
  const [stackErrors, setStackErrors] = useState<Record<string, string>>({})
  const [exporting, setExporting] = useState<Stack | null>(null)
  const [guideId, setGuideId] = useState<string | null>(null)
  const [tab, setTab] = useState<TabId | null>(null)
  const [firstTab, setFirstTab] = useState<TabId | null>(null)
  const [labFocus, setLabFocus] = useState<string | null>(null)
  const [version, setVersion] = useState<string | null>(null)
  const [settings, setSettings] = useState<Settings | null>(null)
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [d, p, s] = await Promise.all([api.docker(), api.providers(), api.stacks()])
      setDocker(d)
      setProviders(p)
      setStackList(s)
      setNeedsToken(false)
      setLoaded(true)
      // Decide the landing page once: the guide on a fresh start, the control room when things already run.
      setFirstTab((cur) => cur ?? startTab(p.some((x) => x.state === 'running')))
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setNeedsToken(true)
    }
  }, [])

  useEffect(() => {
    api.health().then((h) => setVersion(h.version ?? null)).catch(() => {})
    refresh()
    const id = setInterval(refresh, 3000)
    return () => clearInterval(id)
  }, [refresh])

  // Settings live in the data folder; the browser copy only makes the first paint right. The server wins once it has an answer.
  useEffect(() => {
    system
      .settings()
      .then(async (s) => {
        setSettings(s)
        const localLang = readPref('lang')
        const localTheme = loadTheme()
        if (s.lang) setLang(s.lang)
        else if (localLang === 'es' || localLang === 'en') await system.saveSettings({ lang: localLang }).then(setSettings)
        if (s.theme !== 'auto') setTheme(s.theme)
        else if (localTheme !== 'auto') await system.saveSettings({ theme: localTheme }).then(setSettings)
      })
      .catch(() => {})
  }, [])

  // One automatic look for a new version when the app opens. The server decides whether it is due (mode, once a day).
  useEffect(() => {
    updatesApi
      .status()
      .then((s) => {
        setUpdateStatus(s)
        if (s.mode === 'auto') updatesApi.check(false).then(setUpdateStatus).catch(() => {})
      })
      .catch(() => {})
  }, [])

  const changeSettings = async (patch: Partial<Pick<Settings, 'lang' | 'theme' | 'updateMode'>>) => {
    const next = await system.saveSettings(patch)
    setSettings(next)
    if (patch.lang) { setLang(patch.lang); writePref('lang', patch.lang) }
    if (patch.theme) { setTheme(patch.theme); writePref('theme', patch.theme) }
    if (patch.updateMode) updatesApi.status().then(setUpdateStatus).catch(() => {})
  }

  useEffect(() => applyTheme(theme), [theme])
  useEffect(() => {
    document.documentElement.lang = lang
  }, [lang])

  const act = async (p: Provider, action: ProviderAction) => {
    if (action === 'deleteData') {
      const ok = window.confirm(
        lang === 'es'
          ? `Se borrarán para siempre los datos guardados de ${p.name}. ¿Continuar?`
          : `The saved data of ${p.name} will be deleted for good. Continue?`,
      )
      if (!ok) return
    }
    setBusy((b) => new Set(b).add(p.id))
    setErrors((e) => ({ ...e, [p.id]: '' }))
    try {
      if (action === 'persistOn') await api.setPersist(p.id, true)
      else if (action === 'persistOff') await api.setPersist(p.id, false)
      else if (action === 'deleteData') await api.deleteData(p.id)
      else if (action === 'initStart') {
        await variables.init(p.id)
        await api.start(p.id)
      }
      else await api[action](p.id)
    } catch (e) {
      setErrors((prev) => ({ ...prev, [p.id]: e instanceof Error ? e.message : String(e) }))
    } finally {
      setBusy((b) => {
        const n = new Set(b)
        n.delete(p.id)
        return n
      })
      refresh()
    }
  }

  /** Starts providers one after the other and reports the first failure, so a recipe can say what went wrong. */
  const startMany = async (ids: string[]): Promise<string | null> => {
    let failure: string | null = null
    for (const id of ids) {
      try {
        await api.start(id)
      } catch (e) {
        failure = `${id}: ${e instanceof Error ? e.message : String(e)}`
        break
      }
    }
    await refresh()
    return failure
  }

  const stopMany = async (ids: string[]) => {
    for (const id of [...ids].reverse()) {
      try {
        await api.stop(id)
      } catch {
        /* already stopped */
      }
    }
    await refresh()
  }

  const runStack = async (s: Stack, action: 'stackUp' | 'stackDown') => {
    setStackBusy(s.id)
    setStackErrors((e) => ({ ...e, [s.id]: '' }))
    try {
      const result = await api[action](s.id)
      setStackErrors((e) => ({ ...e, [s.id]: failures(result).join(' · ') }))
    } catch (e) {
      setStackErrors((prev) => ({ ...prev, [s.id]: e instanceof Error ? e.message : String(e) }))
    } finally {
      setStackBusy(null)
      refresh()
    }
  }

  if (needsToken) return <TokenGate t={t} onDone={refresh} />

  const current: TabId = tab ?? firstTab ?? 'guide'
  const running = countRunning(providers)
  const go = (next: TabId) => {
    setTab(next)
    setGuideId(null)
    window.scrollTo({ top: 0 })
  }
  const guideProvider = guideId ? providers.find((p) => p.id === guideId) : undefined
  const item = itemOf(current)
  const isGuide = current === 'guide'

  const setLanguage = (next: Lang) => {
    setLang(next)
    writePref('lang', next)
    system.saveSettings({ lang: next }).then(setSettings).catch(() => {})
  }
  const cycleTheme = () => {
    const next = nextTheme(theme)
    setTheme(next)
    writePref('theme', next)
    system.saveSettings({ theme: next }).then(setSettings).catch(() => {})
  }

  return (
    <div className="app">
      <a className="skip" href="#main">
        {t('skipToContent')}
      </a>

      <aside className="rail">
        <div className="brand">
          <Logo />
          <div>
            <b>Nimbo</b>
            <span>{t('tagline')}</span>
          </div>
        </div>

        <nav aria-label="Nimbo">
          {NAV.map((g) => (
            <div className="nav-group" key={g.group}>
              <h2>{t(g.group)}</h2>
              {g.items.map((i) => (
                <button key={i.id} className={current === i.id ? 'nav-item on' : 'nav-item'} aria-current={current === i.id ? 'page' : undefined} onClick={() => go(i.id)}>
                  {t(i.label)}
                  {i.id === 'infra' && running > 0 && <span className="count">{running}</span>}
                  {i.id === 'updates' && updateDot(updateStatus) && <span className="count" title={t('updateAvailable')} aria-label={t('updateAvailable')}>●</span>}
                </button>
              ))}
            </div>
          ))}
        </nav>

        <div className="rail-foot">
          <span className="chip" data-ok={docker?.available ?? false}>
            <i className={docker?.available ? 'led on' : 'dot'} />
            {docker?.available ? `${t('dockerUp')} ${docker.version ?? ''}` : t('dockerDown')}
          </span>
          <span className="chip">
            <b>{running}</b>&nbsp;{t('running')}
          </span>
          <div className="toggles">
            <button className="ghost small" onClick={() => setLanguage(lang === 'es' ? 'en' : 'es')} aria-label="language">
              {lang === 'es' ? 'EN' : 'ES'}
            </button>
            <button className="ghost small" onClick={cycleTheme} aria-label={t('themeLabel')} title={t('themeLabel')}>
              {theme === 'auto' ? t('themeAuto') : theme === 'light' ? t('themeLight') : t('themeDark')}
            </button>
          </div>
          <p className="version">
            Nimbo {version ? `v${version}` : 'dev'} ·{' '}
            <a href="https://github.com/Sebas1705/Nimbo" target="_blank" rel="noreferrer noopener">
              GitHub
            </a>
          </p>
        </div>
      </aside>

      <main id="main" className="stage" tabIndex={-1}>
        {docker && !docker.available && (
          <div className="banner" role="alert">
            {t('dockerHint')}
          </div>
        )}

        <header className="page-head">
          <p className="eyebrow">{t(groupOf(current))}</p>
          <h1>{isGuide ? t('heroTitle') : t(item.label)}</h1>
          <p className="purpose">{isGuide ? t('heroBody') : t(item.purpose)}</p>
        </header>

        <HelpBox key={`${current}-${lang}`} tab={current} lang={lang} t={t} />

        {current === 'guide' && (
          <GuideView
            t={t}
            lang={lang}
            docker={docker}
            providers={providers}
            startMany={startMany}
            stop={stopMany}
            go={go}
            openLab={(id) => {
              setLabFocus(id)
              go('labs')
            }}
            openGuide={setGuideId}
          />
        )}
        {current === 'infra' && loaded && (
          <InfraView
            t={t}
            lang={lang}
            docker={docker}
            providers={providers}
            stackList={stackList}
            busy={busy}
            errors={errors}
            stackBusy={stackBusy}
            stackErrors={stackErrors}
            act={act}
            runStack={runStack}
            onPanel={(kind, provider) => setPanel({ kind, provider })}
            onExport={setExporting}
            onGuide={setGuideId}
          />
        )}
        {current === 'inspector' && <InspectorView t={t} />}
        {current === 'kube' && <KubeView t={t} />}
        {current === 'traces' && <TracesView t={t} />}
        {current === 'chaos' && <ChaosView t={t} />}
        {current === 'labs' && <LabsView t={t} lang={lang} focus={labFocus} />}
        {current === 'fidelity' && <FidelityView t={t} />}
        {current === 'activity' && <ActivityView t={t} lang={lang} />}
        {current === 'settings' && <SettingsView t={t} lang={lang} providers={providers} settings={settings} onSettings={changeSettings} onChanged={refresh} />}
        {current === 'updates' && <UpdatesView t={t} lang={lang} status={updateStatus} onStatus={setUpdateStatus} />}
        {current === 'offline' && <OfflineView t={t} lang={lang} />}
      </main>

      {panel && <SidePanel panel={panel} t={t} onClose={() => setPanel(null)} />}
      {exporting && <ExportPanel stack={exporting} t={t} lang={lang} hasAws={exporting.providers.includes('floci-aws')} onClose={() => setExporting(null)} />}
      {guideProvider && (
        <ProviderGuidePanel
          provider={guideProvider}
          guide={guideFor(guideProvider.id)}
          t={t}
          lang={lang}
          starting={busy.has(guideProvider.id)}
          dockerUp={docker?.available === true}
          onStart={() => act(guideProvider, 'start')}
          onClose={() => setGuideId(null)}
          go={go}
        />
      )}
    </div>
  )
}
