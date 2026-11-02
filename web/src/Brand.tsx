import { useState } from 'react'
import { api, tokenStore } from './api'
import type { Key } from './i18n'
import { Feedback } from './ui'

export function Logo({ size = 34 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" className="logo">
      <defs>
        <linearGradient id="nimbo-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#9db4ff" />
          <stop offset="1" stopColor="#6ee7c8" />
        </linearGradient>
      </defs>
      <path fill="url(#nimbo-g)" d="M9 24a6 6 0 0 1-.6-12A8 8 0 0 1 24 10.5 6.5 6.5 0 0 1 23.5 24Z" />
    </svg>
  )
}

/** Shown when the API is protected by a token (NIMBO_API_TOKEN) and the browser does not have it yet. */
export function TokenGate({ t, onDone }: { t: (k: Key) => string; onDone: () => void }) {
  const [value, setValue] = useState('')
  const [bad, setBad] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    tokenStore.set(value.trim())
    try {
      await api.providers()
      setBad(false)
      onDone()
    } catch {
      setBad(true)
    }
  }

  return (
    <div className="gate">
      <form onSubmit={submit} className="gate-card">
        <Logo size={44} />
        <h1>{t('tokenTitle')}</h1>
        <p>{t('tokenBody')}</p>
        <input type="password" autoFocus value={value} onChange={(e) => setValue(e.target.value)} aria-label="token" />
        <Feedback error={bad ? t('unauthorized') : ''} />
        <button className="primary" type="submit">
          {t('tokenSave')}
        </button>
      </form>
    </div>
  )
}
