import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, FileText, Folder } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'
import { fileApi, type DirEntry, type Root } from '../../files/api'
import { Chip } from '@planner/shared'
import { useAutolink } from '../../autolink/context'

interface Node { key: string; root: Root; rel: string; entry: DirEntry }

/** Project files: the allow-listed roots, directories loaded on expand (GET files/list). A file opens in the File viewer drawer. */
export function FileTree({ filter }: { filter: string }) {
  const autolink = useAutolink()
  const [roots, setRoots] = useState<Root[] | null>(null)
  const [error, setError] = useState('')
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const [kids, setKids] = useState<Record<string, DirEntry[] | 'loading' | string>>({})
  useEffect(() => {
    let live = true
    fileApi.roots().then((r) => { if (live) setRoots(r.roots) }, (e: Error & { status?: number }) => { if (live) setError(e.status === 401 || e.status === 403 ? 'Unlock your operator session to browse project files.' : 'Could not load the project roots.') })
    return () => { live = false }
  }, [])
  const load = (key: string, root: Root, rel: string) => {
    setKids((k) => ({ ...k, [key]: 'loading' }))
    fileApi.list(root.id, rel).then(
      (d) => setKids((k) => ({ ...k, [key]: d.entries })),
      (e: Error) => setKids((k) => ({ ...k, [key]: e.message })),
    )
  }
  const toggle = (key: string, root: Root, rel: string) => {
    const next = !open[key]
    setOpen((o) => ({ ...o, [key]: next }))
    if (next && !kids[key]) load(key, root, rel)
  }
  const openFile = (n: Node) => {
    // path_display is '~/...' under the service home or absolute; files/stat and files/content accept both as-is.
    autolink.openDrawer({ kind: 'path', canonical: `${n.root.path_display.replace(/\/$/, '')}/${n.rel}` })
  }
  const rows = (root: Root, rel: string, depth: number): React.ReactNode => {
    const key = `${root.id}:${rel}`
    const k = kids[key]
    if (k === 'loading') return <p role="status" style={{ paddingLeft: 6 + depth * 16 }} className="py-1 text-xs text-fg-muted">Loading…</p>
    if (typeof k === 'string') return <p role="alert" style={{ paddingLeft: 6 + depth * 16 }} className="py-1 text-xs text-danger">{k} <button type="button" onClick={() => load(key, root, rel)} className="underline">Retry</button></p>
    if (!k) return null
    const shown = k.filter((e) => e.name.toLowerCase().includes(filter.toLowerCase()) || e.kind === 'dir')
    if (!shown.length) return <p style={{ paddingLeft: 6 + depth * 16 }} className="py-1 text-xs text-fg-muted">Empty.</p>
    return shown.map((e) => {
      const r = rel ? `${rel}/${e.name}` : e.name
      const ck = `${root.id}:${r}`
      return (
        <div key={ck} role="none">
          <div role="treeitem" aria-expanded={e.kind === 'dir' ? !!open[ck] : undefined} aria-selected={false} tabIndex={0} style={{ paddingLeft: 6 + depth * 16 }}
            onClick={() => (e.kind === 'dir' ? toggle(ck, root, r) : openFile({ key: ck, root, rel: r, entry: e }))}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); if (e.kind === 'dir') toggle(ck, root, r); else openFile({ key: ck, root, rel: r, entry: e }) } }}
            className={cn('flex cursor-pointer items-center gap-2 rounded-control px-1.5 py-[5px] text-xs text-fg-secondary hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring')}>
            {e.kind === 'dir' ? (open[ck] ? <ChevronDown aria-hidden className="size-3" /> : <ChevronRight aria-hidden className="size-3" />) : <FileText aria-hidden className="size-3 text-fg-muted" />}
            <span className="min-w-0 flex-1 truncate">{e.name}</span>
            {e.modified && <Chip square><span className="sr-only">modified</span><span aria-hidden>M</span></Chip>}
          </div>
          {e.kind === 'dir' && open[ck] && <div role="group">{rows(root, r, depth + 1)}</div>}
        </div>
      )
    })
  }
  if (error) return <p role="status" className="text-xs text-fg-muted">{error}</p>
  if (!roots) return <p role="status" className="text-xs text-fg-muted">Loading project files…</p>
  if (!roots.length) return <p className="text-xs text-fg-muted">No project roots are shared with this host. Add one in Settings.</p>
  return (
    <div role="tree" aria-label="Project files" className="flex flex-col">
      {roots.map((r) => {
        const key = `${r.id}:`
        return (
          <div key={r.id} role="none">
            <div role="treeitem" aria-expanded={!!open[key]} aria-selected={false} tabIndex={0} onClick={() => toggle(key, r, '')}
              onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); toggle(key, r, '') } }}
              className="flex cursor-pointer items-center gap-2 rounded-control px-1.5 py-[5px] text-xs text-fg hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
              {open[key] ? <ChevronDown aria-hidden className="size-3" /> : <ChevronRight aria-hidden className="size-3" />}<Folder aria-hidden className="size-3 text-fg-muted" /><span className="min-w-0 flex-1 truncate">{r.label}</span><span className="font-mono text-caption text-fg-faint">{r.path_display}</span>
            </div>
            {open[key] && <div role="group">{rows(r, '', 1)}</div>}
          </div>
        )
      })}
    </div>
  )
}
