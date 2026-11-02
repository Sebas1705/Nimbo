import type { DockerStatus, Provider } from './api'
import { registry } from './api'
import type { Key, Lang } from './i18n'
import type { TabId } from './nav'
import './registry.css'
import { ComposeSection } from './registry/ComposeSection'
import { ImagesSection } from './registry/ImagesSection'
import { SourcesSection } from './registry/SourcesSection'
import { useLoad } from './ui'

type T = (k: Key) => string

interface Props {
  t: T
  lang: Lang
  docker: DockerStatus | null
  providers: Provider[]
  /** Reload the provider list in the app (an own image shows up in Infrastructure as soon as it is saved). */
  onChanged: () => void
  go: (tab: TabId) => void
  /** Open the screen with one own image already being edited (the link on its card in Infrastructure). */
  focusImage?: string | null
}

/** The registry: own images, linked repositories and compose profiles. Each section lives in `registry/`. */
export function RegistryView({ lang, docker, providers, onChanged, go, focusImage }: Props) {
  const images = useLoad(registry.images.list)
  const sources = useLoad(registry.sources.list)
  const compose = useLoad(registry.compose.list, 5000)

  const reloadImages = () => { images.reload(); onChanged() }

  return (
    <div className="registry">
      <ImagesSection lang={lang} docker={docker} providers={providers} load={images} reload={reloadImages} go={go} focus={focusImage ?? null} />
      <SourcesSection lang={lang} load={sources} reload={sources.reload} composes={compose.data ?? []} />
      <ComposeSection lang={lang} docker={docker} load={compose} sources={sources.data ?? []} reload={compose.reload} goSources={() => document.getElementById('reg-sources')?.scrollIntoView({ behavior: 'smooth' })} />
    </div>
  )
}
