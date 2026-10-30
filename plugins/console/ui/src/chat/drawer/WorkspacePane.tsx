import { useRef, useState } from 'react'
import { FileText, Image as ImageIcon, Link2, Plus, X } from 'lucide-react'
import { Chip } from '@planner/shared'
import { Act } from '../cards/Block'
import { cn } from '@hollis-labs/design-components'
import type { WorkspaceItem } from './model'

const ICON = { file: FileText, image: ImageIcon, link: Link2, ref: Link2 } as const

function Toggle({ on, label, onChange }: { on: boolean; label: string; onChange: (v: boolean) => void }) {
  return (
    <button type="button" role="switch" aria-checked={on} aria-label={label} onClick={() => onChange(!on)} className={cn('relative h-3 w-[22px] rounded-full focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', on ? 'bg-fg/80' : 'bg-surface-hover')}>
      <i className={cn('absolute top-0.5 size-2 rounded-full', on ? 'left-3 bg-bg' : 'left-0.5 bg-fg-muted')} />
    </button>
  )
}

/** Files, links, references and images for this session, with a share-with-agent toggle. Shared items appear in the composer strip. */
export function WorkspacePane({ ws }: { ws: { items: WorkspaceItem[]; loading: boolean; error: string; share: (id: string, v: boolean) => void; remove: (id: string) => void; addFile: (f: File) => Promise<void>; addLink: (u: string) => Promise<void> } }) {
  const input = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  const drop = (e: React.DragEvent) => {
    e.preventDefault(); setOver(false)
    for (const f of Array.from(e.dataTransfer.files)) void ws.addFile(f)
    const url = e.dataTransfer.getData('text/uri-list') || e.dataTransfer.getData('text/plain')
    if (!e.dataTransfer.files.length && /^https?:\/\//.test(url)) void ws.addLink(url.trim())
  }
  const paste = async () => {
    try { const t = (await navigator.clipboard.readText()).trim(); if (/^https?:\/\//.test(t)) await ws.addLink(t) }
    catch { /* clipboard refused; the drop tile still works */ }
  }
  const shared = ws.items.filter((i) => i.shared).length
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-label text-fg-muted">Workspace · {shared} shared with the agent this session</span>
        <span className="flex-1" />
        <input ref={input} type="file" multiple hidden onChange={(e) => { for (const f of Array.from(e.target.files ?? [])) void ws.addFile(f); e.target.value = '' }} />
        <Act onClick={() => input.current?.click()}><Plus aria-hidden className="size-3" />Add</Act>
        <Act onClick={() => void paste()}>Paste</Act>
      </div>
      {ws.error && <p role="alert" className="text-xs text-danger">{ws.error}</p>}
      <div onDragOver={(e) => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)} onDrop={drop} className="grid grid-cols-4 gap-2 max-sm:grid-cols-2">
        {ws.loading && <p role="status" className="col-span-full text-xs text-fg-muted">Loading the Workspace…</p>}
        {ws.items.map((i) => {
          const Icon = ICON[i.type]
          return (
            <div key={i.id} className="group relative flex flex-col gap-1.5 rounded-control border border-border p-2 text-xs text-fg-secondary">
              <div className="grid h-11 place-items-center rounded-sm bg-surface text-fg-muted"><Icon aria-hidden className="size-4" /></div>
              <span className="truncate text-fg" title={i.name}>{i.name}</span>
              <div className="flex items-center justify-between font-mono text-caption text-fg-muted"><span>{i.type}</span><Toggle on={i.shared} label={`Share ${i.name} with the agent`} onChange={(v) => ws.share(i.id, v)} /></div>
              <button type="button" aria-label={`Remove ${i.name}`} onClick={() => ws.remove(i.id)} className="absolute right-1 top-1 hidden size-5 place-items-center rounded-sm bg-bg-elevated text-fg-muted hover:text-fg group-hover:grid group-focus-within:grid"><X aria-hidden className="size-3" /></button>
            </div>
          )
        })}
        <button type="button" onClick={() => input.current?.click()} className={cn('flex min-h-[88px] items-center justify-center rounded-control border border-dashed border-border p-2 text-xs text-fg-muted hover:text-fg-secondary', over && 'bg-selection text-fg')}>
          + Drop files, links, images
        </button>
      </div>
      {!ws.loading && ws.items.length === 0 && !ws.error && <Chip className="self-start">Nothing shared yet</Chip>}
    </>
  )
}
