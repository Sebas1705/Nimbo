import { useState } from 'react'
import type { Key } from './i18n'

/** A snippet people copy and paste into a terminal, so it is a block with one obvious button. */
export function CodeBlock({ title, code, t }: { title?: string; code: string; t: (k: Key) => string }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked: the text is still selectable */
    }
  }

  return (
    <figure className="code">
      <figcaption>
        <span>{title}</span>
        <button className="ghost small" onClick={copy}>
          {copied ? t('copied') : t('copy')}
        </button>
      </figcaption>
      <pre>
        <code>{code}</code>
      </pre>
    </figure>
  )
}
