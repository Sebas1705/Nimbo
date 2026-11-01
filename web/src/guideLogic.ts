import type { DockerStatus, Provider } from './api'
import type { Recipe } from './guides'

export type RecipeState = 'idle' | 'partial' | 'up'

export interface RecipeStatus {
  state: RecipeState
  running: number
  total: number
  /** Providers that still have to be started, in recipe order. */
  missing: string[]
}

/** How much of a recipe is already up, from the live provider list. A provider the backend does not know counts as missing. */
export function recipeStatus(recipe: Pick<Recipe, 'providers'>, providers: Provider[]): RecipeStatus {
  const live = new Map(providers.map((p) => [p.id, p.state]))
  const missing = recipe.providers.filter((id) => live.get(id) !== 'running')
  const running = recipe.providers.length - missing.length
  const state: RecipeState = missing.length === 0 ? 'up' : running === 0 ? 'idle' : 'partial'
  return { state, running, total: recipe.providers.length, missing }
}

/** The port people connect to: the first one the provider publishes. */
export function primaryPort(provider: Pick<Provider, 'ports'>): number | null {
  return provider.ports[0] ?? null
}

/** Ports after the first, shown small so a provider with eleven of them does not drown the card. */
export function extraPorts(provider: Pick<Provider, 'ports'>): number[] {
  return provider.ports.slice(1)
}

export interface StepStatus {
  /** Step 1: Docker answers. Step 2: something runs. The rest are things to do, not states to detect. */
  done: boolean[]
  /** The first step that is not done yet, 0-based; 5 when the detectable ones are done. */
  current: number
}

export function stepStatus(docker: DockerStatus | null, providers: Provider[]): StepStatus {
  const dockerOk = docker?.available === true
  const anything = providers.some((p) => p.state === 'running')
  const done = [dockerOk, dockerOk && anything, false, false, false]
  const firstOpen = done.findIndex((d) => !d)
  return { done, current: firstOpen === -1 ? 5 : Math.min(firstOpen, 2) }
}

export function runningCount(providers: Provider[]): number {
  return providers.filter((p) => p.state === 'running').length
}
