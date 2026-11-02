export type RunState = 'running' | 'stopped' | 'not_created' | 'error'

export interface Provider {
  id: string
  name: string
  cloud: 'aws' | 'azure' | 'gcp' | 'oci' | 'other'
  description: string
  image: string
  ports: number[]
  state: RunState
  detail?: string | null
  /** The emulator has data worth keeping between runs. */
  persistable?: boolean
  /** The "keep my data" switch is on. */
  persist?: boolean
  /** A Docker volume with saved data exists right now. */
  hasData?: boolean
  /** Required settings with no value yet: the emulator cannot start until they are initialized. */
  needsSetup?: string[]
  /** One of your own images from the Registry (not an emulator that ships with Nimbo). */
  custom?: boolean
}

export interface DockerStatus {
  available: boolean
  version?: string | null
  error?: string | null
}

export interface Health {
  status: string
  authRequired: boolean
  version?: string | null
}

export interface Stack {
  id: string
  description: string
  providers: string[]
  state: 'running' | 'partial' | 'stopped'
  running: number
  total: number
  /** Made in the app: it can be edited and deleted. */
  custom?: boolean
}

export interface StackList {
  stacks: Stack[]
  problems: string[]
}

export interface StackAction {
  id: string
  action: 'up' | 'down'
  results: Array<{ id: string; ok: boolean; error?: string | null }>
}

export type ExportFormat = 'env' | 'tofu' | 'awscli' | 'compose' | 'composeenv' | 'spring' | 'dotnet'

export interface ExportResult {
  format: ExportFormat
  filename: string
  content: string
}

export interface Bucket { name: string; created?: string | null }
export interface S3Object { key: string; size: number; lastModified?: string | null }
export interface ObjectList { bucket: string; objects: S3Object[]; truncated: boolean }
export interface ObjectPreview { bucket: string; key: string; text: string; truncated: boolean }
export interface QueueInfo { name: string; url: string; messages: number | null; inFlight: number | null; delayed: number | null }
export interface QueueMessage { id: string; body: string; receiveCount: number | null; sentAt: number | null }
export interface TableInfo { name: string; status: string | null; items: number | null; keys: string[] }
export interface TableItems { table: string; items: unknown[]; count: number }
export interface PubSub { project: string; topics: Array<{ name: string }>; subscriptions: Array<{ name: string; topic?: string | null }> }

export interface ChaosParam { key: string; label: string; unit: string; min: number; max: number; default: number }
export interface ChaosPreset { id: string; label: string; description: string; params: ChaosParam[] }
export interface Toxic { name: string; type: string; toxicity: number; attributes: Record<string, number> }
export interface ChaosTarget {
  id: string
  name: string
  upstreamPort: number
  proxyPort: number
  endpoint: string
  active: boolean
  enabled: boolean
  toxics: Toxic[]
}
export interface ChaosView { available: boolean; presets: ChaosPreset[]; targets: ChaosTarget[] }

export type FitStatus = 'full' | 'partial' | 'missing' | 'offline'
export interface FitStep { name: string; status: 'pass' | 'fail' | 'skipped'; detail?: string | null; ms: number }
export interface FitSuite { id: string; cloud: string; name: string; status: FitStatus; steps: FitStep[]; ms: number }
export interface FitRef { cloud: string; id: string; name: string }
export interface FitState { running: boolean; updatedAt: number | null; catalog: FitRef[]; results: FitSuite[]; summary: Record<string, number> }

export interface L10n { en: string; es: string }
export interface Lab {
  id: string
  level: number
  stack?: string | null
  title: L10n
  summary: L10n
  steps: L10n[]
  hints: L10n[]
  solution: Array<{ title?: L10n | null; code: string }>
  checks: Array<{ label: L10n }>
}
export interface LabList { labs: Lab[]; problems: string[] }
export interface LabVerify { id: string; passed: boolean; results: Array<{ index: number; ok: boolean; detail?: string | null }> }

export interface KubeNode { name: string; ready: boolean; version?: string | null; cpu?: string | null; memory?: string | null }
export interface KubePod { namespace: string; name: string; status: string; ready: string; restarts: number; node?: string | null; startedAt?: string | null }
export interface KubeDeployment { namespace: string; name: string; ready: number; desired: number }
export interface KubeService { namespace: string; name: string; type: string; clusterIp?: string | null; ports: string[]; nodePorts: number[] }
export interface KubeOverview {
  available: boolean
  reason?: string | null
  version?: string | null
  namespaces: string[]
  nodes: KubeNode[]
  deployments: KubeDeployment[]
  pods: KubePod[]
  services: KubeService[]
}
export interface KubeSample { id: string; title: string; description: string; manifest: string }

export interface TraceSummary { traceId: string; service: string; operation: string; startedAt: number; durationMicros: number; spans: number; errors: number }
export interface Span { id: string; parentId?: string | null; service: string; operation: string; depth: number; offsetMicros: number; durationMicros: number; error: boolean }
export interface TraceDetail { traceId: string; startedAt: number; durationMicros: number; spans: Span[] }

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

const TOKEN_KEY = 'nimbo.token'

export const tokenStore = {
  get(): string {
    try {
      return localStorage.getItem(TOKEN_KEY) ?? ''
    } catch {
      return ''
    }
  },
  set(value: string) {
    try {
      localStorage.setItem(TOKEN_KEY, value)
    } catch {
      /* private mode: token only lives for this session */
    }
  },
}

const json = (body: unknown, method = 'POST'): RequestInit => ({ method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = tokenStore.get()
  const res = await fetch(path, {
    ...init,
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...init.headers },
  })
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, body.error ?? res.statusText)
  return body as T
}

export const api = {
  health: () => request<Health>('/api/health'),
  docker: () => request<DockerStatus>('/api/docker'),
  providers: () => request<Provider[]>('/api/providers'),
  start: (id: string) => request<Provider>(`/api/providers/${id}/start`, { method: 'POST' }),
  stop: (id: string) => request<Provider>(`/api/providers/${id}/stop`, { method: 'POST' }),
  remove: (id: string) => request<void>(`/api/providers/${id}`, { method: 'DELETE' }),
  setPersist: (id: string, enabled: boolean) => request<Settings>(`/api/providers/${id}/persist`, json({ enabled })),
  deleteData: (id: string) => request<void>(`/api/providers/${id}/data?confirm=yes`, { method: 'DELETE' }),
  logs: (id: string) => request<{ lines: string }>(`/api/providers/${id}/logs?tail=300`),
  env: (id: string, secrets = false) => request<{ dotenv: string }>(`/api/providers/${id}/env${secrets ? '?secrets=true' : ''}`),
  stacks: () => request<StackList>('/api/stacks'),
  saveStack: (id: string, description: string, providers: string[]) => request<Stack>(`/api/stacks/${id}`, json({ description, providers }, 'PUT')),
  deleteStack: (id: string) => request<void>(`/api/stacks/${id}`, { method: 'DELETE' }),
  stackUp: (id: string) => request<StackAction>(`/api/stacks/${id}/up`, { method: 'POST' }),
  stackDown: (id: string) => request<StackAction>(`/api/stacks/${id}/down`, { method: 'POST' }),
  stackExport: (id: string, format: ExportFormat, o: { chaos?: boolean; persist?: boolean; secrets?: boolean } = {}) =>
    request<ExportResult>(`/api/stacks/${id}/export?format=${format}${o.chaos ? '&chaos=true' : ''}${o.persist ? '&persist=true' : ''}${o.secrets ? '&secrets=true' : ''}`),
}

/** Human-readable failures of a stack action, e.g. "redis: port busy". Empty when everything worked. */
export function failures(action: StackAction): string[] {
  return action.results.filter((r) => !r.ok).map((r) => `${r.id}: ${r.error ?? 'failed'}`)
}

const seg = encodeURIComponent

export const inspect = {
  buckets: () => request<Bucket[]>('/api/inspect/aws/s3/buckets'),
  objects: (b: string) => request<ObjectList>('/api/inspect/aws/s3/buckets/' + seg(b) + '/objects'),
  preview: (b: string, key: string) => request<ObjectPreview>('/api/inspect/aws/s3/buckets/' + seg(b) + '/object?key=' + seg(key)),
  queues: () => request<QueueInfo[]>('/api/inspect/aws/sqs/queues'),
  messages: (q: string) => request<{ queue: string; messages: QueueMessage[] }>('/api/inspect/aws/sqs/queues/' + seg(q) + '/messages'),
  tables: () => request<TableInfo[]>('/api/inspect/aws/dynamodb/tables'),
  items: (t: string) => request<TableItems>('/api/inspect/aws/dynamodb/tables/' + seg(t) + '/items'),
  pubsub: (project: string) => request<PubSub>('/api/inspect/gcp/pubsub?project=' + seg(project)),
}

export const chaos = {
  view: () => request<ChaosView>('/api/chaos'),
  apply: (id: string, preset: string, params: Record<string, number>) =>
    request<ChaosTarget>(`/api/chaos/targets/${seg(id)}/${seg(preset)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ params }),
    }),
  clear: (id: string) => request<ChaosTarget>(`/api/chaos/targets/${seg(id)}/toxics`, { method: 'DELETE' }),
  removeToxic: (id: string, name: string) =>
    request<ChaosTarget>(`/api/chaos/targets/${seg(id)}/toxics/${seg(name)}`, { method: 'DELETE' }),
  reset: () => request<ChaosView>('/api/chaos/reset', { method: 'POST' }),
}

const k8s = '/api/k8s'

export const kube = {
  overview: () => request<KubeOverview>(k8s),
  samples: () => request<KubeSample[]>(`${k8s}/samples`),
  apply: (manifest: string) => request<{ output: string }>(`${k8s}/apply`, json({ manifest })),
  scale: (ns: string, name: string, replicas: number) => request<{ output: string }>(`${k8s}/namespaces/${seg(ns)}/deployments/${seg(name)}/scale`, json({ replicas })),
  remove: (kind: 'pod' | 'deployment' | 'service', ns: string, name: string) =>
    request<{ output: string }>(`${k8s}/namespaces/${seg(ns)}/${kind}/${seg(name)}`, { method: 'DELETE' }),
  logs: (ns: string, pod: string) => request<{ output: string }>(`${k8s}/namespaces/${seg(ns)}/pods/${seg(pod)}/logs?tail=300`),
  kubeconfig: () => request<{ filename: string; content: string }>(`${k8s}/kubeconfig`),
}

export const traces = {
  services: () => request<string[]>('/api/traces/services'),
  list: (service: string) => request<TraceSummary[]>(`/api/traces?service=${seg(service)}&limit=30`),
  detail: (id: string) => request<TraceDetail>(`/api/traces/${seg(id)}`),
  sample: (error = false) => request<{ traceId: string; service: string }>(`/api/traces/sample${error ? '?error=true' : ''}`, { method: 'POST' }),
}

/** 45 µs, 120 ms, 1.50 s. */
export function formatMicros(us: number): string {
  if (us < 1_000) return `${us} µs`
  if (us < 1_000_000) return `${Math.round(us / 1_000)} ms`
  return `${(us / 1_000_000).toFixed(2)} s`
}

/** How worrying a pod status is, for colouring: Running is fine, waiting states are transitional, the rest is bad. */
export function podTone(status: string): 'ok' | 'warn' | 'bad' {
  if (['Running', 'Succeeded', 'Completed'].includes(status)) return 'ok'
  if (['Pending', 'ContainerCreating', 'PodInitializing', 'Terminating', 'Init:0/1'].includes(status)) return 'warn'
  return 'bad'
}

/** Position and width of a span in a waterfall, as percentages that never leave the 0..100 box. */
export function barStyle(span: Pick<Span, 'offsetMicros' | 'durationMicros'>, total: number): { left: number; width: number } {
  if (total <= 0) return { left: 0, width: 100 }
  const left = Math.min(100, Math.max(0, (span.offsetMicros / total) * 100))
  const width = Math.min(100 - left, Math.max(0.8, (span.durationMicros / total) * 100))
  return { left, width }
}

export const fidelity = {
  state: () => request<FitState>('/api/fidelity'),
  run: (cloud?: string) => request<FitState>('/api/fidelity/run' + (cloud ? `?cloud=${cloud}` : ''), { method: 'POST' }),
}

export const labs = {
  list: () => request<LabList>('/api/labs'),
  verify: (id: string) => request<LabVerify>(`/api/labs/${seg(id)}/verify`, { method: 'POST' }),
}

/** Share of fidelity steps that passed over everything that was run, 0..100 (0 when nothing ran). */
export function coverage(results: FitSuite[]): number {
  const steps = results.flatMap((r) => r.steps)
  if (steps.length === 0) return 0
  return Math.round((steps.filter((s) => s.status === 'pass').length / steps.length) * 100)
}

const DONE_KEY = 'nimbo.labs.done'

/** Completed lab ids, kept in this browser only. */
export const labProgress = {
  load(): Set<string> {
    try {
      const raw = JSON.parse(localStorage.getItem(DONE_KEY) ?? '[]')
      return new Set(Array.isArray(raw) ? raw.filter((x): x is string => typeof x === 'string') : [])
    } catch {
      return new Set()
    }
  },
  save(done: Set<string>) {
    try {
      localStorage.setItem(DONE_KEY, JSON.stringify([...done]))
    } catch {
      /* private mode: progress only lives for this session */
    }
  },
}

/** What is wrong with a target, for the status pill: outage beats faults beats healthy. */
export function chaosState(t: ChaosTarget): 'outage' | 'faults' | 'healthy' {
  if (t.active && !t.enabled) return 'outage'
  return t.toxics.length > 0 ? 'faults' : 'healthy'
}

/** Short size label: 512 B, 1.5 KB, 3.2 MB. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export const CLOUD_ORDER: Provider['cloud'][] = ['aws', 'azure', 'gcp', 'oci', 'other']

export function groupByCloud(providers: Provider[]): Array<[Provider['cloud'], Provider[]]> {
  return CLOUD_ORDER.map((c) => [c, providers.filter((p) => p.cloud === c)] as [Provider['cloud'], Provider[]]).filter(
    ([, list]) => list.length > 0,
  )
}

export function countRunning(providers: Provider[]): number {
  return providers.filter((p) => p.state === 'running').length
}

// ---- Data folder: settings, progress, statistics, secrets, backups -------------------------------------------------

export interface Settings {
  lang: 'es' | 'en' | null
  theme: Theme
  updateMode: 'auto' | 'manual'
  persist: string[]
  lastUpdateCheck: number | null
}
type Theme = 'auto' | 'light' | 'dark'
export interface LabRecord { completedAt: number | null; attempts: number; firstAttemptAt: number | null; lastAttemptAt: number | null }
export interface ProviderUsage { starts: number; stops: number; upSeconds: number; lastStartedAt: number | null }
export interface JournalEntry { at: number; kind: string; subject: string; detail?: string | null }
export interface FidelityRun { at: number; durationMs: number; suites: Record<string, string>; steps: number; passed: number }
export interface StatsView {
  stats: { since: number; providers: Record<string, ProviderUsage>; counters: Record<string, number> }
  journal: JournalEntry[]
  fidelity: FidelityRun[]
}
export interface DataInfo { location: string | null; files: Record<string, number>; distribution: 'binary' | 'desktop' | 'docker' }
export interface SecretItem { name: string; set: boolean; usedBy: string[]; password: boolean }
export interface SecretsView { available: boolean; reason: string | null; items: SecretItem[] }
export type ResetWhat = 'stats' | 'progress' | 'history' | 'all'

export const system = {
  settings: () => request<Settings>('/api/settings'),
  saveSettings: (patch: Partial<Pick<Settings, 'lang' | 'theme' | 'updateMode'>>) => request<Settings>('/api/settings', json(patch, 'PUT')),
  progress: () => request<{ labs: Record<string, LabRecord> }>('/api/progress'),
  importProgress: (ids: string[]) => request<{ labs: Record<string, LabRecord> }>('/api/progress/import', json({ ids })),
  stats: () => request<StatsView>('/api/stats?limit=60'),
  data: () => request<DataInfo>('/api/data'),
  exportData: () => request<unknown>('/api/data/export'),
  importData: (bundle: unknown) => request<{ restored: string[] }>('/api/data/import', json(bundle)),
  reset: (what: ResetWhat) => request<void>(`/api/data/reset?what=${what}&confirm=yes`, { method: 'POST' }),
  secrets: () => request<SecretsView>('/api/secrets'),
  setSecret: (name: string, value: string) => request<void>(`/api/secrets/${seg(name)}`, json({ value }, 'PUT')),
  generateSecret: (name: string) => request<{ value: string }>(`/api/secrets/${seg(name)}/generate`, { method: 'POST' }),
  deleteSecret: (name: string) => request<void>(`/api/secrets/${seg(name)}`, { method: 'DELETE' }),
}

// ---- Updates and offline images ------------------------------------------------------------------------------------

export interface UpdateAsset { name: string; size: number; url: string }
export interface UpdateRelease { tag: string; version: string; notes: string; publishedAt: string | null; assets: UpdateAsset[] }
export interface UpdateFile { version: string; name: string; sha256: string; verified: boolean; source: 'github' | 'file'; size: number; at: number }
export interface UpdateStatus {
  current: string | null
  distribution: 'binary' | 'desktop' | 'docker'
  mode: 'auto' | 'manual'
  checkedAt: number | null
  latest: UpdateRelease | null
  available: boolean
  asset: UpdateAsset | null
  files: UpdateFile[]
  canInstall: boolean
  error: string | null
  manual: string | null
}

export const updates = {
  status: () => request<UpdateStatus>('/api/updates'),
  /** `force` is the button; without it the server respects the update mode and the daily limit. */
  check: (force: boolean) => request<UpdateStatus>(`/api/updates/check${force ? '?force=true' : ''}`, { method: 'POST' }),
  download: (version: string) => request<UpdateFile>('/api/updates/download', json({ version })),
  importFile: (path: string, sha256?: string) => request<UpdateFile>('/api/updates/import', json({ path, sha256: sha256 || null })),
  install: (version: string) => request<{ message: string }>('/api/updates/install', json({ version, confirm: true })),
  clean: () => request<{ deleted: number }>('/api/updates/files', { method: 'DELETE' }),
}

export interface ImageState { provider: string; image: string; present: boolean; pack: string | null }
export interface PackFile { name: string; path: string; size: number; pack: string | null; verified: boolean | null }
export interface ImagesView { docker: boolean; images: ImageState[]; packs: Array<{ id: string; images: string[] }>; files: PackFile[]; folders: string[] }

export const images = {
  status: () => request<ImagesView>('/api/images'),
  load: (path: string) => request<{ loaded: string[] }>('/api/images/load', json({ path })),
  pull: (provider: string) => request<void>('/api/images/pull', json({ provider })),
}

// ---- Emulator variables ---------------------------------------------------------------------------------------------

export interface VarInfo {
  name: string
  container: string
  secret: boolean
  required: boolean
  canGenerate: boolean
  default: string | null
  /** Only takes effect when the emulator's data is first created. */
  fixedAtCreation: boolean
  source: 'environment' | 'stored' | 'unset'
  set: boolean
}
export interface ProviderVars { provider: string; name: string; vars: VarInfo[] }
export interface VariablesView { available: boolean; reason: string | null; providers: ProviderVars[] }

export const variables = {
  list: () => request<VariablesView>('/api/variables'),
  /** Fills in every setting of an emulator that has none. Optional ones only when asked. */
  init: (provider: string, optional = false) => request<{ created: string[] }>(`/api/providers/${provider}/variables/init${optional ? '?optional=true' : ''}`, { method: 'POST' }),
  /** Reads one value back. A deliberate action: it is written to the journal (the name, never the value). */
  reveal: (name: string) => request<{ value: string; source: string }>(`/api/secrets/${seg(name)}/reveal`, { method: 'POST' }),
  /** Recreates a running emulator with the values it has now (its data volume stays). */
  apply: (provider: string) => request<{ applied: boolean; note: string | null }>(`/api/providers/${provider}/apply`, { method: 'POST' }),
}

// ---- Registry: own images, linked sources, compose profiles ---------------------------------------------------------

export interface ImageBody {
  name: string
  image: string
  ports: number[]
  env: Array<{ key: string; value: string }>
  secretKeys: string[]
  command: string[]
  volume: string | null
  notes: string
}
export interface ImageDef extends ImageBody {
  id: string
  secrets: Array<{ key: string; name: string; set: boolean }>
  custom: true
}

export interface SourceBody {
  kind: 'local' | 'git'
  path?: string
  url?: string
  branch?: string | null
}
export interface SourceDef {
  id: string
  kind: 'local' | 'git'
  path: string | null
  url: string | null
  branch: string | null
  dir: string
  status: 'ok' | 'missing' | 'not-cloned' | 'error'
  head: string | null
  currentBranch: string | null
  dirty: boolean
  behind: number | null
  syncedAt: number | null
  error: string | null
}

/** source "" means the path is absolute; otherwise it is relative to that source's folder. */
export interface ComposeFileRef { source: string; path: string }
export interface ComposeBody {
  name: string
  purpose: string
  files: ComposeFileRef[]
  profiles: string[]
  envFile: ComposeFileRef | null
}
export interface ComposeFileStatus { path: string; status: 'ok' | 'missing' | 'changed'; sha256: string | null }
export interface ComposeService { name: string; state: string; health: string | null }
export interface ComposeDef extends ComposeBody {
  id: string
  state: { running: number; total: number; services: ComposeService[] }
  fileStatus: ComposeFileStatus[]
  envFileStatus: ComposeFileStatus | null
  lastUpSha: Record<string, string> | null
  lastUpAt: number | null
}

const reg = '/api/registry'

export const registry = {
  images: {
    list: () => request<ImageDef[]>(`${reg}/images`),
    save: (id: string, body: ImageBody) => request<ImageDef>(`${reg}/images/${seg(id)}`, json(body, 'PUT')),
    remove: (id: string) => request<void>(`${reg}/images/${seg(id)}`, { method: 'DELETE' }),
  },
  sources: {
    list: () => request<SourceDef[]>(`${reg}/sources`),
    save: (id: string, body: SourceBody) => request<SourceDef>(`${reg}/sources/${seg(id)}`, json(body, 'PUT')),
    remove: (id: string) => request<void>(`${reg}/sources/${seg(id)}`, { method: 'DELETE' }),
    sync: (id: string) => request<SourceDef>(`${reg}/sources/${seg(id)}/sync`, { method: 'POST' }),
  },
  compose: {
    list: () => request<ComposeDef[]>(`${reg}/compose`),
    save: (id: string, body: ComposeBody) => request<ComposeDef>(`${reg}/compose/${seg(id)}`, json(body, 'PUT')),
    remove: (id: string) => request<void>(`${reg}/compose/${seg(id)}`, { method: 'DELETE' }),
    validate: (id: string) => request<{ ok: boolean; message: string }>(`${reg}/compose/${seg(id)}/validate`, { method: 'POST' }),
    up: (id: string) => request<ComposeDef>(`${reg}/compose/${seg(id)}/up`, { method: 'POST' }),
    down: (id: string, volumes = false) => request<ComposeDef>(`${reg}/compose/${seg(id)}/down?volumes=${volumes}`, { method: 'POST' }),
    logs: (id: string, service = '', tail = 200) => request<{ lines: string }>(`${reg}/compose/${seg(id)}/logs?tail=${tail}${service ? `&service=${seg(service)}` : ''}`),
    file: (id: string, index: number) => request<{ path: string; content: string; truncated: boolean }>(`${reg}/compose/${seg(id)}/file?index=${index}`),
  },
}

/** What is still needed before an emulator can start, as a short list of names. */
export function setupMissing(p: Pick<Provider, 'needsSetup'>): string[] {
  return p.needsSetup ?? []
}
