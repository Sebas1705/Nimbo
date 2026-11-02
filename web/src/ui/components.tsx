import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import type { Lang } from '../i18n'
import './kit.css'
import { CONFIRM_MS, confirmStep, type ConfirmState } from './confirmLogic'
import { useCopy } from './hooks'
import { pick, type Tone } from './text'

// ---------------------------------------------------------------------------------------------------------------------
// Feedback: what an action is doing, what it said, what went wrong
// ---------------------------------------------------------------------------------------------------------------------

export function Spinner() {
  return <i className="spinner" aria-hidden="true" />
}

/** The three states of an action in one place: progress, error (announced) and success. Renders nothing when quiet. */
export function Feedback({ working = '', error = '', done = '' }: { working?: string; error?: string; done?: string }) {
  return (
    <>
      {working && (
        <p className="reg-progress" role="status">
          <Spinner />
          {working}
        </p>
      )}
      {error && (
        <p className="err" role="alert">
          {error}
        </p>
      )}
      {done && (
        <p className="ok-note" role="status">
          {done}
        </p>
      )}
    </>
  )
}

/** A message that stays on the page: something to know, to be careful about, or that went wrong. */
export function Notice({ tone = 'warn', title, children, action }: { tone?: 'warn' | 'bad' | 'ok'; title?: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className={`notice notice-${tone}${action ? ' notice-with-action' : ''}`} role={tone === 'bad' ? 'alert' : 'status'}>
      <div className="notice-text">
        {title && <b>{title}</b>}
        {children && <div>{children}</div>}
      </div>
      {action && <div className="actions">{action}</div>}
    </div>
  )
}

export function Pill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span className={`pill tone-${tone}`}>
      <i className={tone === 'ok' ? 'led on' : 'dot'} />
      {children}
    </span>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Structure
// ---------------------------------------------------------------------------------------------------------------------

/** A titled block with optional actions on the right: the unit every screen is made of. */
export function Card({ title, actions, tone, className, children }: { title?: ReactNode; actions?: ReactNode; tone?: 'highlight'; className?: string; children: ReactNode }) {
  return (
    <section className={['sys-card', tone, className].filter(Boolean).join(' ')}>
      {(title || actions) && (
        <header className="card-head">
          {title && <h3>{title}</h3>}
          {actions && <div className="actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  )
}

/** A big number with a small label, for dashboards. */
export function Stat({ title, value, note }: { title: string; value: ReactNode; note?: ReactNode }) {
  return (
    <div className="sys-card stat">
      <h3>{title}</h3>
      <b className="big">{value}</b>
      {note && <small>{note}</small>}
    </div>
  )
}

/** What a section is for, and the one thing to do first. Shown instead of an empty list. */
export function EmptyState({ title, body, action, onAction }: { title: string; body: string; action?: string; onAction?: () => void }) {
  return (
    <div className="reg-empty">
      <h3>{title}</h3>
      <p>{body}</p>
      {action && onAction && (
        <button className="primary" onClick={onAction}>
          {action}
        </button>
      )}
    </div>
  )
}

/** Name and value pairs, aligned: the way to show the facts of a thing. */
export function Facts({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="reg-facts">
      {items.map(([k, v]) => (
        <div key={k} className="fact">
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Buttons that do more than click
// ---------------------------------------------------------------------------------------------------------------------

/**
 * A risky button that asks first, in place, instead of a pop-up of the browser: the first press turns it into a question
 * with the explicit action and a cancel. It withdraws itself after a few seconds, on Escape and when focus leaves it.
 */
export function ConfirmButton({
  lang, label, question, confirmLabel, onConfirm, disabled, className = 'ghost danger', title, children,
}: {
  lang: Lang
  label: string
  /** The sentence that asks; say what will happen and what will not. */
  question: string
  confirmLabel?: string
  onConfirm: () => void
  disabled?: boolean
  className?: string
  /** Tooltip of the button while it is not asking (for example why it is disabled). */
  title?: string
  /** Extra controls inside the question, for example a checkbox ("also delete the volumes"). */
  children?: ReactNode
}) {
  const [state, setState] = useState<ConfirmState>('idle')
  const box = useRef<HTMLDivElement>(null)
  const ask = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (state !== 'asking') return
    const id = setTimeout(() => setState((s) => confirmStep(s, 'timeout').state), CONFIRM_MS)
    return () => clearTimeout(id)
  }, [state])

  const step = (event: Parameters<typeof confirmStep>[1]) => {
    const next = confirmStep(state, event)
    setState(next.state)
    if (next.run) onConfirm()
  }

  if (state === 'idle') {
    return (
      <button className={className} disabled={disabled} title={title} onClick={() => step('press')}>
        {label}
      </button>
    )
  }
  return (
    <div
      ref={box}
      className="confirm"
      role="alertdialog"
      aria-label={question}
      // Focus stays inside the question when you press its text (a label, a checkbox caption) instead of leaving the page.
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key !== 'Escape') return
        // Escape closes only the question, not the drawer it may sit in.
        e.nativeEvent.stopPropagation()
        step('cancel')
      }}
      onBlur={(e) => {
        if (!box.current?.contains(e.relatedTarget as Node | null)) step('blur')
      }}
    >
      <span>{question}</span>
      {children}
      <button ref={ask} autoFocus className="danger-solid" onClick={() => step('confirm')}>
        {confirmLabel ?? pick(lang, 'Sí, continuar', 'Yes, continue')}
      </button>
      <button className="ghost" onClick={() => step('cancel')}>
        {pick(lang, 'Cancelar', 'Cancel')}
      </button>
    </div>
  )
}

/** Copies a text and says "Copied" for a moment. */
export function CopyButton({ lang, text, className = 'ghost small', label }: { lang: Lang; text: string; className?: string; label?: string }) {
  const { copied, copy } = useCopy()
  return (
    <button className={className} onClick={() => copy(text)} aria-live="polite">
      {copied ? pick(lang, 'Copiado', 'Copied') : (label ?? pick(lang, 'Copiar', 'Copy'))}
    </button>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------------------------------------------------

/** A labelled control with its hint or inline error, linked with aria-describedby so screen readers read it. */
export function Field({
  label, error, hint, children,
}: {
  label: string
  error?: string
  hint?: string
  children: (p: { id: string; 'aria-invalid': boolean; 'aria-describedby'?: string }) => ReactNode
}) {
  const id = useId()
  const msgId = `${id}-msg`
  return (
    <div className="rf">
      <label htmlFor={id}>{label}</label>
      {children({ id, 'aria-invalid': !!error, 'aria-describedby': error || hint ? msgId : undefined })}
      {(error || hint) && (
        <small id={msgId} className={error ? 'err' : 'hint'}>
          {error || hint}
        </small>
      )}
    </div>
  )
}

/** Values as removable chips: type one and press Enter or comma. */
export function ChipInput({
  label, values, onChange, placeholder, error, hint, lang, inputMode,
}: {
  label: string
  values: string[]
  onChange: (v: string[]) => void
  placeholder?: string
  error?: string
  hint?: string
  lang: Lang
  inputMode?: 'numeric' | 'text'
}) {
  const [text, setText] = useState('')
  const commit = () => {
    const parts = text.split(',').map((x) => x.trim()).filter(Boolean)
    if (parts.length === 0) return
    onChange([...values, ...parts.filter((p, i) => !values.includes(p) && parts.indexOf(p) === i)])
    setText('')
  }
  return (
    <Field label={label} error={error} hint={hint}>
      {(a) => (
        <div className="chip-input">
          {values.length > 0 && (
            <ul className="chips">
              {values.map((v) => (
                <li key={v}>
                  {v}
                  <button type="button" className="chip-x" aria-label={pick(lang, `Quitar ${v}`, `Remove ${v}`)} onClick={() => onChange(values.filter((x) => x !== v))}>
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="chip-add">
            <input
              {...a}
              value={text}
              inputMode={inputMode}
              placeholder={placeholder}
              onChange={(e) => setText(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ',') {
                  e.preventDefault()
                  commit()
                } else if (e.key === 'Backspace' && !text && values.length > 0) onChange(values.slice(0, -1))
              }}
            />
            <button type="button" className="ghost small" onClick={commit} disabled={!text.trim()}>
              {pick(lang, 'Añadir', 'Add')}
            </button>
          </div>
        </div>
      )}
    </Field>
  )
}

/** Remembers which fields were touched so errors do not shout before people have typed anything. */
export function useShown(errors: Record<string, string>) {
  const [tried, setTried] = useState(false)
  const [touched, setTouched] = useState<Set<string>>(new Set())
  const touch = (k: string) => setTouched((s) => (s.has(k) ? s : new Set(s).add(k)))
  const shown = (k: string) => (tried || touched.has(k) ? errors[k] ?? '' : '')
  const submit = (go: () => void) => {
    setTried(true)
    if (Object.keys(errors).length === 0) go()
  }
  return { shown, touch, submit, tried }
}

/** Save and cancel, the same everywhere. */
export function FormActions({
  lang, saving, onSave, onCancel, saveLabel, invalid,
}: {
  lang: Lang
  saving: boolean
  onSave: () => void
  onCancel: () => void
  saveLabel: string
  invalid: boolean
}) {
  return (
    <div className="actions">
      <button className="primary" disabled={saving} onClick={onSave}>
        {saving ? pick(lang, 'Guardando…', 'Saving…') : saveLabel}
      </button>
      <button className="ghost" disabled={saving} onClick={onCancel}>
        {pick(lang, 'Cancelar', 'Cancel')}
      </button>
      {invalid && (
        <small className="err" role="status">
          {pick(lang, 'Revisa los campos marcados.', 'Check the highlighted fields.')}
        </small>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------------------------------------------------
// Drawer
// ---------------------------------------------------------------------------------------------------------------------

/** A panel that slides over the right of the screen: a title, its actions, a close button, Escape to leave. */
export function Drawer({
  title, label, lang, onClose, actions, wide, className, children,
}: {
  title: ReactNode
  /** The accessible name when the title is not plain text. */
  label?: string
  lang: Lang
  onClose: () => void
  actions?: ReactNode
  wide?: boolean
  className?: string
  children: ReactNode
}) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      before?.focus?.()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return (
    <aside className={['panel', wide && 'panel-wide', className].filter(Boolean).join(' ')} role="dialog" aria-label={label ?? (typeof title === 'string' ? title : undefined)}>
      <div className="panel-head">
        <h3>{title}</h3>
        <div className="actions">
          {actions}
          <button ref={closeRef} className="ghost" onClick={onClose}>
            {pick(lang, 'Cerrar', 'Close')}
          </button>
        </div>
      </div>
      {children}
    </aside>
  )
}
