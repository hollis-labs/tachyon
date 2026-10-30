import { useCallback, useEffect, useRef, useState } from 'react'
import { CHAT_BASE, chatFetch, chatJSON } from '../roster'

export type DrawerMode = 'scratch' | 'terminal' | 'workspace'
export type DrawerSize = 'quarter' | 'half' | 'full'

export interface WorkspaceItem {
  id: string
  type: 'file' | 'link' | 'ref' | 'image'
  name: string
  mime?: string | null
  size_bytes?: number | null
  url?: string | null
  ref?: string | null
  shared: boolean
  added_by?: string
}

const enc = encodeURIComponent

/** Workspace items of one thread (files, links, references, images) and which are shared with the agent. */
export function useWorkspace(ref: string | undefined) {
  const [items, setItems] = useState<WorkspaceItem[]>([])
  const [loading, setLoading] = useState(!!ref)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    if (!ref) { setItems([]); setLoading(false); return }
    try {
      const d = await chatJSON<{ items: WorkspaceItem[] }>(`/threads/${enc(ref)}/workspace`)
      setItems(d.items ?? []); setError('')
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not load the Workspace.') }
    finally { setLoading(false) }
  }, [ref])
  useEffect(() => { setLoading(!!ref); void load() }, [load, ref])

  const share = useCallback(async (id: string, shared: boolean) => {
    if (!ref) return
    setItems((xs) => xs.map((x) => (x.id === id ? { ...x, shared } : x)))
    try { await chatJSON(`/threads/${enc(ref)}/workspace/${enc(id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ shared }) }) }
    catch (e) { setItems((xs) => xs.map((x) => (x.id === id ? { ...x, shared: !shared } : x))); setError(e instanceof Error ? e.message : 'Could not change sharing.') }
  }, [ref])
  const remove = useCallback(async (id: string) => {
    if (!ref) return
    try { await chatJSON(`/threads/${enc(ref)}/workspace/${enc(id)}`, { method: 'DELETE' }); setItems((xs) => xs.filter((x) => x.id !== id)) }
    catch (e) { setError(e instanceof Error ? e.message : 'Could not remove the item.') }
  }, [ref])
  const addFile = useCallback(async (file: File) => {
    if (!ref) return
    const form = new FormData(); form.append('file', file)
    const res = await chatFetch(`${CHAT_BASE}/threads/${enc(ref)}/workspace`, { method: 'POST', body: form })
    if (!res.ok) { setError(res.status === 413 ? `${file.name} is too large.` : res.status === 415 ? `${file.name}: that file type is not allowed.` : `Could not add ${file.name}.`); return }
    setError(''); await load()
  }, [ref, load])
  const addLink = useCallback(async (url: string) => {
    if (!ref) return
    try { await chatJSON(`/threads/${enc(ref)}/workspace`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ type: 'link', name: url.replace(/^https?:\/\//, '').slice(0, 80), url }) }); setError(''); await load() }
    catch (e) { setError(e instanceof Error ? e.message : 'Could not add the link.') }
  }, [ref, load])
  return { items, loading, error, share, remove, addFile, addLink }
}

/** Scratchpad body of one thread: local notes saved to the server with the revision check, never sent unless referenced. */
export function useScratchpad(ref: string | undefined) {
  const [body, setBody] = useState('')
  const [state, setState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [error, setError] = useState('')
  const rev = useRef(0)
  const last = useRef('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    if (!ref) return
    let live = true
    chatJSON<{ body: string; revision: number }>(`/threads/${enc(ref)}/scratchpad`).then(
      (d) => { if (live) { setBody(d.body); last.current = d.body; rev.current = d.revision } },
      (e) => { if (live) setError(e instanceof Error ? e.message : 'Could not load the scratchpad.') },
    )
    return () => { live = false }
  }, [ref])
  const save = useCallback(async (text: string) => {
    if (!ref || text === last.current) return
    setState('saving')
    try {
      const d = await chatJSON<{ body: string; revision: number }>(`/threads/${enc(ref)}/scratchpad`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body: text, expected_revision: rev.current }) })
      rev.current = d.revision; last.current = text; setState('saved'); setError('')
    } catch (e) { setState('error'); setError(e instanceof Error ? e.message : 'Could not save the scratchpad. Your text is kept here.') }
  }, [ref])
  const change = useCallback((text: string) => {
    setBody(text)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => void save(text), 800)
  }, [save])
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])
  return { body, change, state, error, retry: () => void save(body) }
}

/** Where the scratchpad lives: the drawer (default) or the aside panel. */
export function useScratchPlacement(): 'drawer' | 'aside' {
  const [p, setP] = useState<'drawer' | 'aside'>('drawer')
  useEffect(() => {
    let live = true
    chatJSON<{ scratchpad_placement?: string }>('/settings').then((s) => { if (live && s.scratchpad_placement === 'aside') setP('aside') }, () => undefined)
    return () => { live = false }
  }, [])
  return p
}
