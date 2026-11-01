import { useState } from 'react'
import { TAB_HELP } from './guides'
import { helpOpen, setHelpOpen } from './prefs'
import type { Key, Lang } from './i18n'
import type { TabId } from './nav'

/** The plain-words explanation at the top of each screen. It remembers whether you folded it away. */
export function HelpBox({ tab, lang, t }: { tab: TabId; lang: Lang; t: (k: Key) => string }) {
  const [open, setOpen] = useState(() => helpOpen(tab))
  const points = TAB_HELP[tab].points

  return (
    <details
      className="help"
      open={open}
      onToggle={(e) => {
        const next = (e.currentTarget as HTMLDetailsElement).open
        if (next !== open) {
          setOpen(next)
          setHelpOpen(tab, next)
        }
      }}
    >
      <summary>
        <span className="help-mark" aria-hidden="true">
          ?
        </span>
        {t('helpHow')}
      </summary>
      <ul>
        {points.map((p, i) => (
          <li key={i}>{p[lang]}</li>
        ))}
      </ul>
    </details>
  )
}
