import type { ReactNode } from 'react'

/** What a `useLoad` hands to a section. */
export type Load<D> = { data: D | null; error: string; reload: () => void }

/** The title of a section and, once it has content, the button that adds one more. */
export function SectionHead({ id, title, addLabel, onAdd }: { id: string; title: string; addLabel?: string; onAdd?: () => void }) {
  return (
    <div className="section-head">
      <h2 className="eyebrow" id={id}>{title}</h2>
      {addLabel && onAdd && <button className="ghost small" onClick={onAdd}>{addLabel}</button>}
    </div>
  )
}

/** The load error or the "still loading" dots of a section. */
export function LoadState({ load }: { load: Load<unknown> }) {
  return (
    <>
      {load.error && <p className="err" role="alert">{load.error}</p>}
      {!load.data && !load.error && <p className="muted">…</p>}
    </>
  )
}

/** Nodes separated by a space. */
export const spaced = (nodes: ReactNode[]): ReactNode[] => nodes.reduce<ReactNode[]>((acc, x, i) => (i ? [...acc, ' ', x] : [x]), [])
