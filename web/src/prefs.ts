/** Per-browser preferences. Everything here is a convenience: the app must work when storage is blocked. */

const PREFIX = 'nimbo.'

export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(PREFIX + key)
  } catch {
    return null
  }
}

export function writePref(key: string, value: string): void {
  try {
    localStorage.setItem(PREFIX + key, value)
  } catch {
    /* private mode or blocked storage: the choice just lasts until reload */
  }
}

export type Theme = 'auto' | 'light' | 'dark'

const THEMES: Theme[] = ['auto', 'light', 'dark']

export function loadTheme(): Theme {
  const v = readPref('theme')
  return THEMES.includes(v as Theme) ? (v as Theme) : 'auto'
}

/** auto -> light -> dark -> auto */
export function nextTheme(current: Theme): Theme {
  return THEMES[(THEMES.indexOf(current) + 1) % THEMES.length]
}

/** `auto` removes the attribute so the system preference decides; the others pin it. */
export function applyTheme(theme: Theme, root: Pick<HTMLElement, 'setAttribute' | 'removeAttribute'> = document.documentElement): void {
  if (theme === 'auto') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', theme)
}

/** The help box of a screen stays however the person left it; it starts open so first-timers see it. */
/**
 * Whether the note of a screen starts open: only when the person left it open. A new screen starts with it folded and a
 * dot on its button, so the content is what you see first; the Guide already explains the basics.
 */
export function helpOpen(tab: string): boolean {
  return readPref(`help.${tab}`) === 'open'
}

/** True until the person has opened or closed the note of this screen once: the button shows a dot until then. */
export function helpUnseen(tab: string): boolean {
  return readPref(`help.${tab}`) === null
}

export function setHelpOpen(tab: string, open: boolean): void {
  writePref(`help.${tab}`, open ? 'open' : 'closed')
}
