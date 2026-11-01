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

export type ExportFormat = 'env' | 'tofu' | 'awscli'

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
  logs: (id: string) => request<{ lines: string }>(`/api/providers/${id}/logs?tail=300`),
  env: (id: string) => request<{ dotenv: string }>(`/api/providers/${id}/env`),
  stacks: () => request<StackList>('/api/stacks'),
  stackUp: (id: string) => request<StackAction>(`/api/stacks/${id}/up`, { method: 'POST' }),
  stackDown: (id: string) => request<StackAction>(`/api/stacks/${id}/down`, { method: 'POST' }),
  stackExport: (id: string, format: ExportFormat, chaos = false) =>
    request<ExportResult>(`/api/stacks/${id}/export?format=${format}${chaos ? '&chaos=true' : ''}`),
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

const json = (body: unknown): RequestInit => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
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
