import type { DockerStatus, Provider } from './api'
import type { Lang } from './i18n'
import type { Recipe } from './guides'
import { pick } from './ui'

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

/** Same rules as the server: a lowercase name of up to 41 characters, at least one emulator, a short description. */
export const STACK_NAME = /^[a-z0-9][a-z0-9-]{0,40}$/
export const STACK_DESCRIPTION_MAX = 200

/** What is wrong with a stack draft, by field; empty when it can be saved. */
export function stackErrors(lang: Lang, d: { id: string; description: string; providers: string[]; isNew: boolean }): Record<string, string> {
  const errors: Record<string, string> = {}
  if (d.isNew && !STACK_NAME.test(d.id.trim())) {
    errors.id = d.id.trim()
      ? pick(lang, 'Usa minúsculas, números y guiones; empieza por letra o número; hasta 41 caracteres.', 'Use lowercase letters, numbers and dashes; start with a letter or number; up to 41 characters.')
      : pick(lang, 'Pon un nombre al stack.', 'Give the stack a name.')
  }
  if (d.providers.length === 0) errors.providers = pick(lang, 'Elige al menos un emulador.', 'Pick at least one emulator.')
  if (d.description.trim().length > STACK_DESCRIPTION_MAX) errors.description = pick(lang, `Como máximo ${STACK_DESCRIPTION_MAX} caracteres.`, `At most ${STACK_DESCRIPTION_MAX} characters.`)
  return errors
}
