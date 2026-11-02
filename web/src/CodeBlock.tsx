import type { Key, Lang } from './i18n'
import { CopyButton } from './ui'

/** The language a screen that only received `t` is showing, read from a word every dictionary has. */
export const langOf = (t: (k: Key) => string): Lang => (t('close') === 'Cerrar' ? 'es' : 'en')

/** A snippet people copy and paste into a terminal, so it is a block with one obvious button. */
export function CodeBlock({ title, code, t, lang }: { title?: string; code: string; t: (k: Key) => string; lang?: Lang }) {
  return (
    <figure className="code">
      <figcaption>
        <span>{title}</span>
        <CopyButton lang={lang ?? langOf(t)} text={code} />
      </figcaption>
      <pre>
        <code>{code}</code>
      </pre>
    </figure>
  )
}
