import { useCallback, useEffect, useRef, useState } from 'react'
import type { ObjectPage, ObjectRecord, OwnedBuilderClient } from './owned-builder-client'

export function useOwnedRecords(client: OwnedBuilderClient, kind: string) {
  const [rows, setRows] = useState<ObjectRecord[]>(() => client.cached(kind)?.items ?? [])
  const [next, setNext] = useState<string | undefined>(() => client.cached(kind)?.next_after_id)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const pages = useRef(new Map<string, ObjectPage>())
  const epoch = useRef(0), active = useRef(true), running = useRef(false)
  const read = useCallback(async (after?: string) => {
    if (running.current) return
    running.current = true; setLoading(true)
    const at = epoch.current
    try {
      const cursors = after !== undefined ? [after] : pages.current.size ? [...pages.current.keys()] : ['']
      const result = await Promise.all(cursors.map(async cursor => [cursor, await client.list(kind, cursor)] as const))
      if (!active.current || at !== epoch.current) return
      result.forEach(([cursor, page]) => pages.current.set(cursor, page))
      const current = [...pages.current.values()]
      setRows([...new Map(current.flatMap(page => page.items).map(row => [row.id, row])).values()])
      setNext(current.at(-1)?.next_after_id); setError('')
    } catch (e) { if (active.current && at === epoch.current) setError(e instanceof Error ? e.message : 'Records unavailable') }
    finally { running.current = false; if (active.current) setLoading(false) }
  }, [client, kind])
  useEffect(() => {
    active.current = true
    void read()
    const refresh = () => { if (document.visibilityState !== 'hidden') void read() }
    const timer = setInterval(refresh, 30_000)
    window.addEventListener('focus', refresh)
    return () => { active.current = false; clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [read])
  const commit = (row: ObjectRecord) => {
    epoch.current++
    for (const page of pages.current.values()) page.items = page.items.map(item => item.id === row.id ? row : item)
    setRows(current => [row, ...current.filter(item => item.id !== row.id)])
  }
  return { rows, next, error, loading, refresh: () => read(), more: () => next ? read(next) : Promise.resolve(), commit }
}
