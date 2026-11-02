import { useCallback, useEffect, useRef, useState } from 'react'
import { message } from './text'

/** Runs a loader now, again on demand, and (when asked) every few seconds. A failed load keeps the last data. */
export function useLoad<D>(load: () => Promise<D>, everyMs = 0): { data: D | null; error: string; reload: () => void } {
  const [data, setData] = useState<D | null>(null)
  const [error, setError] = useState('')
  const loadRef = useRef(load)
  loadRef.current = load
  const reload = useCallback(() => {
    loadRef
      .current()
      .then((d) => {
        setData(d)
        setError('')
      })
      .catch((e) => setError(message(e)))
  }, [])
  useEffect(() => {
    reload()
    if (!everyMs) return
    const id = setInterval(reload, everyMs)
    return () => clearInterval(id)
  }, [reload, everyMs])
  return { data, error, reload }
}

/**
 * One action with its own progress text, success note and error, so a slow step does not freeze the rest of the screen.
 * `run` takes the progress text first (it may be empty); the function may return the success note.
 */
export function useAction() {
  const [working, setWorking] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const run = async (progress: string, fn: () => Promise<string | void>) => {
    setWorking(progress)
    setBusy(true)
    setError('')
    setDone('')
    try {
      setDone((await fn()) || '')
    } catch (e) {
      setError(message(e))
    } finally {
      setWorking('')
      setBusy(false)
    }
  }
  const clear = () => {
    setError('')
    setDone('')
  }
  return { working, busy, error, done, run, clear }
}

/** Copies text and says so for a moment. Where the clipboard is blocked nothing happens: the text is still selectable. */
export function useCopy(ms = 1500) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), ms)
    } catch {
      /* blocked */
    }
  }
  return { copied, copy }
}
