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
