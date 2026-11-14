import { useEffect, useState } from 'react'
import { FileText, Film, Image as ImageIcon, Globe, ArrowLeft } from 'lucide-react'
import { CAPTION, Chip } from '@planner/shared'
import { cn } from '@hollis-labs/design-components'
import { Act } from '../cards/Block'
import { MessageBody } from '../MessageBody'
import { chatFetch, CHAT_BASE } from '../roster'
import { useWorkspace, pathRefOf, type WorkspaceItem } from '../drawer/model'

export type ArtifactKind = 'html' | 'image' | 'video' | 'audio' | 'markdown' | 'text' | 'other'
const NAME = { html: /\.html?$/i, image: /\.(png|jpe?g|gif|webp|svg|avif)$/i, video: /\.(mp4|webm|mov|m4v)$/i, audio: /\.(mp3|wav|ogg|m4a)$/i, markdown: /\.(md|markdown)$/i, text: /\.(txt|json|ya?ml|ts|tsx|go|css|csv|log)$/i }
/** What an artifact is, from its type, mime and name: decides how the inline preview draws it. */
export function artifactKind(i: Pick<WorkspaceItem, 'type' | 'mime' | 'name'>): ArtifactKind {
  const m = i.mime ?? ''
  if (i.type === 'image' || m.startsWith('image/') || NAME.image.test(i.name)) return 'image'
  if (m === 'text/html' || NAME.html.test(i.name)) return 'html'
  if (m.startsWith('video/') || NAME.video.test(i.name)) return 'video'
  if (m.startsWith('audio/') || NAME.audio.test(i.name)) return 'audio'
  if (m === 'text/markdown' || NAME.markdown.test(i.name)) return 'markdown'
  if (m.startsWith('text/') || /^application\/(json|xml|x-yaml|yaml)$/.test(m) || NAME.text.test(i.name)) return 'text'
  return 'other'
}
const ICON = { html: Globe, image: ImageIcon, video: Film, audio: Film, markdown: FileText, text: FileText, other: FileText } as const
const ROW = 'flex items-center gap-2 rounded-control px-1.5 py-[5px] text-xs text-fg-secondary hover:bg-selection'

/** An item the agent produced: anything in the thread's workspace the user did not add. */
export const isAgentArtifact = (i: WorkspaceItem) => i.added_by !== 'user'

type Loaded = { state: 'loading' } | { state: 'error'; message: string } | { state: 'text'; text: string } | { state: 'blob'; url: string }

function usePreview(threadRef: string | undefined, item: WorkspaceItem, kind: ArtifactKind) {
  const [r, setR] = useState<Loaded>({ state: 'loading' })
  useEffect(() => {
    let alive = true
    let url = ''
    setR({ state: 'loading' })
    if (!threadRef || kind === 'other') { setR({ state: 'error', message: `${item.name} can't be previewed here.` }); return }
    ;(async () => {
      try {
        const res = await chatFetch(`${CHAT_BASE}/threads/${encodeURIComponent(threadRef)}/workspace/${encodeURIComponent(item.id)}/content`)
        if (!res.ok) throw new Error(`Could not open ${item.name} (${res.status}).`)
        if (kind === 'markdown' || kind === 'text' || kind === 'html') { const text = await res.text(); if (alive) setR({ state: 'text', text }); return }
        url = URL.createObjectURL(await res.blob())
        if (alive) setR({ state: 'blob', url })
      } catch (e) { if (alive) setR({ state: 'error', message: e instanceof Error ? e.message : `Could not open ${item.name}.` }) }
    })()
    return () => { alive = false; if (url) URL.revokeObjectURL(url) }
  }, [threadRef, item.id, item.name, kind])
  return r
}

function Preview({ threadRef, item, onClose }: { threadRef?: string; item: WorkspaceItem; onClose: () => void }) {
  const kind = artifactKind(item)
  const r = usePreview(threadRef, item, kind)
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" aria-label="Back to artifacts" onClick={onClose} className="grid size-[22px] place-items-center rounded-control border border-border text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring"><ArrowLeft aria-hidden className="size-3" /></button>
        <Chip square>{item.name}</Chip><span className="flex-1" /><Act kbd="Esc" onClick={onClose}>Close</Act>
      </div>
      {r.state === 'loading' && <p role="status" className="text-xs text-fg-muted">Opening {item.name}…</p>}
      {r.state === 'error' && <p role="alert" className="text-xs text-danger">{r.message}</p>}
      {r.state === 'text' && kind === 'html' && <iframe title={item.name} sandbox="" srcDoc={r.text} className="min-h-[320px] w-full flex-1 rounded-control border border-border bg-bg" />}
      {r.state === 'text' && kind === 'markdown' && <article aria-label={item.name} className="text-control leading-[1.7] [&_:is(h1,h2,h3)]:text-base! [&_:is(h1,h2,h3)]:text-fg!"><MessageBody body={r.text} /></article>}
      {r.state === 'text' && kind === 'text' && <pre aria-label={item.name} className="overflow-auto whitespace-pre-wrap rounded-control border border-border p-2.5 font-mono text-xs text-fg-secondary">{r.text}</pre>}
      {r.state === 'blob' && kind === 'image' && <img src={r.url} alt={item.name} className="max-w-full self-start rounded-control border border-border" />}
      {r.state === 'blob' && kind === 'video' && <video src={r.url} controls aria-label={item.name} className="max-w-full rounded-control border border-border"><track kind="captions" /></video>}
      {r.state === 'blob' && kind === 'audio' && <audio src={r.url} controls aria-label={item.name} className="w-full" />}
      {(r.state === 'error' || r.state === 'blob') && <p className="text-xs text-fg-muted">{r.state === 'blob' ? <a href={r.url} download={item.name} className="underline">Download {item.name}</a> : 'Use Download from the Workspace.'}</p>}
    </>
  )
}

/**
 * chat.aside Artifacts: what the agent produced in this thread. The list is replaced by an inline preview until it is
 * closed. Host paths (type 'ref') open in the File viewer through `onOpenRef`; a document opened from Files shows in
 * the full-window Doc view as before (`doc`).
 */
export function ArtifactsPanel({ threadRef, demo, doc, docPanel, onOpenRef }: { threadRef?: string; demo?: WorkspaceItem[]; doc: unknown; docPanel: React.ReactNode; onOpenRef: (i: WorkspaceItem) => void }) {
  const ws = useWorkspace(threadRef, demo)
  const [open, setOpen] = useState<WorkspaceItem | null>(null)
  useEffect(() => {
    if (!open) return
    const on = (e: KeyboardEvent) => { if (e.key === 'Escape' && !e.defaultPrevented) { e.preventDefault(); setOpen(null) } }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [open])
  if (doc) return <>{docPanel}</>
  if (open) return <Preview threadRef={threadRef} item={open} onClose={() => setOpen(null)} />
  const items = ws.items.filter(isAgentArtifact)
  return (
    <>
      <div className="flex items-center gap-2"><span className={CAPTION}>Artifacts</span><span className="flex-1" />{items.length > 0 && <span className="font-mono text-caption text-fg-muted">{items.length}</span>}</div>
      {ws.loading ? <p role="status" className="text-xs text-fg-muted">Loading artifacts…</p> : ws.error ? <p role="alert" className="text-xs text-danger">{ws.error}</p> : items.length === 0 ? (
        <p className="text-xs text-fg-muted">The agent has not produced any artifacts in this chat yet.</p>
      ) : (
        <ul className="flex flex-col">
          {items.map((i) => { const k = artifactKind(i); const Icon = ICON[k]; return (
            <li key={i.id}><button type="button" onClick={() => (pathRefOf(i) ? onOpenRef(i) : setOpen(i))} className={cn(ROW, 'w-full text-left')}><Icon aria-hidden className="size-3 text-fg-muted" /><span className="min-w-0 flex-1 truncate">{i.name}</span><Chip square>{pathRefOf(i) ? 'file' : k}</Chip></button></li>
          ) })}
        </ul>
      )}
    </>
  )
}
