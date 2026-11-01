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
  stackExport: (id: string, format: ExportFormat) =>
    request<ExportResult>(`/api/stacks/${id}/export?format=${format}`),
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
