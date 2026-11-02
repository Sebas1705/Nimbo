import type { Lang } from '../i18n'

/** The sentence in the person's language. Screens that carry their own copy use this instead of an i18n key. */
export const pick = (lang: Lang, es: string, en: string): string => (lang === 'es' ? es : en)

/** The readable text of anything that was thrown. */
export const message = (e: unknown): string => (e instanceof Error ? e.message : String(e))

/** How worrying something is, for colouring a pill or a note. */
export type Tone = 'ok' | 'warn' | 'bad' | 'idle'
