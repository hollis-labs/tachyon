import { Skeleton } from '@hollis-labs/design-components'
import { Copy, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import type { RefDetailProps } from '../autolink/extensions'
import { contentUrl, denyMessage, errorMessage, type FileKind, type FileStat, fileApi, formatSize, type Frame } from './api'

const BTN =
  'inline-flex items-center gap-1.5 border border-border px-2 py-1 font-mono text-caption uppercase tracking-label text-fg-secondary transition-colors hover:border-fg-faint hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'
const TEXT_KINDS: readonly FileKind[] = ['text', 'json', 'yaml', 'toml', 'csv', 'code']

type Load =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; stat: FileStat }

/** Drawer body for a file path: stat first, then the viewer that fits the kind. */
export function FileViewerPane({ refId }: RefDetailProps) {
  const path = refId.canonical
  const line = typeof refId.meta?.line === 'number' ? refId.meta.line : undefined
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [tick, setTick] = useState(0)
  const reload = useCallback(() => setTick((n) => n + 1), [])

  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-runs the stat on Reload
  useEffect(() => {
    let live = true
    setLoad({ status: 'loading' })
    fileApi.stat(path).then(
      (stat) => live && setLoad({ status: 'ready', stat }),
      (e) => live && setLoad({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [path, tick])

  return (
    <section aria-label={`File ${path.split('/').pop()}`} className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className={BTN} onClick={reload}>
          <RefreshCw className="h-3 w-3" aria-hidden /> Reload
        </button>
        <button type="button" className={BTN} onClick={() => void navigator.clipboard?.writeText(path)}>
          <Copy className="h-3 w-3" aria-hidden /> Copy path
        </button>
      </div>
      {load.status === 'loading' && <Skeleton className="h-40 w-full" />}
      {load.status === 'error' && (
        <p role="alert" className="text-control text-fg-secondary">
          {load.message}
        </p>
      )}
      {load.status === 'ready' && <Body stat={load.stat} path={path} line={line} tick={tick} />}
      <p className="break-all font-mono text-caption text-fg-faint">{path}</p>
    </section>
  )
}

function Body({ stat, path, line, tick }: { stat: FileStat; path: string; line?: number; tick: number }) {
  if (!stat.exists || !stat.allowed) {
    return <p className="text-control text-fg-secondary">{denyMessage(stat.deny_reason, stat.exists)}</p>
  }
  if (stat.type === 'dir') return <p className="text-control text-fg-secondary">This is a folder. Folders are not listed.</p>
  const kind = stat.kind ?? 'binary'
  return (
    <>
      <p className="font-mono text-caption text-fg-faint">
        {[kind, formatSize(stat.size), stat.mtime ? new Date(stat.mtime).toLocaleString() : ''].filter(Boolean).join(' · ')}
      </p>
      {kind === 'image' && <Picture path={path} />}
      {kind === 'html' && <Page path={path} tick={tick} />}
      {kind === 'markdown' && <TextBody path={path} markdown tick={tick} />}
      {TEXT_KINDS.includes(kind) && <TextBody path={path} line={line} tick={tick} />}
      {kind === 'binary' && <p className="text-control text-fg-secondary">No preview for this type. Copy the path to open it elsewhere.</p>}
    </>
  )
}

function Picture({ path }: { path: string }) {
  const [full, setFull] = useState(false)
  const [broken, setBroken] = useState(false)
  if (broken) return <p role="alert" className="text-control text-fg-secondary">The image could not be loaded.</p>
  // SVG is only ever shown through <img>, as the contract requires.
  return (
    <button type="button" className={`block w-full overflow-auto border border-border bg-surface ${full ? '' : 'cursor-zoom-in'}`} aria-label={full ? 'Fit image to width' : 'Show image at full size'} onClick={() => setFull((f) => !f)}>
      <img src={contentUrl(path)} alt={path.split('/').pop() ?? 'image'} onError={() => setBroken(true)} className={full ? 'max-w-none' : 'h-auto w-full'} />
    </button>
  )
}

/** HTML renders in a single-use sandboxed frame (no scripts, no same-origin); Source shows the text. */
function Page({ path, tick }: { path: string; tick: number }) {
  const [view, setView] = useState<'rendered' | 'source'>('rendered')
  const [frame, setFrame] = useState<Frame | { error: string } | null>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-mints the frame on Reload
  useEffect(() => {
    if (view !== 'rendered') return
    let live = true
    setFrame(null)
    fileApi.frame(path).then(
      (f) => live && setFrame(f),
      (e) => live && setFrame({ error: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [path, view, tick])
  return (
    <div className="space-y-2">
      <div role="group" aria-label="HTML view" className="inline-flex border border-border">
        {(['rendered', 'source'] as const).map((v) => (
          <button key={v} type="button" aria-pressed={view === v} onClick={() => setView(v)} className={`px-2 py-1 font-mono text-caption uppercase tracking-label ${view === v ? 'bg-surface-hover text-fg' : 'text-fg-faint'}`}>
            {v === 'rendered' ? 'Rendered' : 'Source'}
          </button>
        ))}
      </div>
      {view === 'source' && <TextBody path={path} tick={tick} />}
      {view === 'rendered' && frame === null && <Skeleton className="h-64 w-full" />}
      {view === 'rendered' && frame && 'error' in frame && (
        <p role="alert" className="text-control text-fg-secondary">{frame.error} Try Source.</p>
      )}
      {view === 'rendered' && frame && 'frame_url' in frame && (
        <iframe title={`Preview of ${path.split('/').pop()}`} src={frame.frame_url} sandbox="" referrerPolicy="no-referrer" className="h-96 w-full border border-border bg-white" />
      )}
    </div>
  )
}

function TextBody({ path, line, markdown, tick }: { path: string; line?: number; markdown?: boolean; tick: number }) {
  const [state, setState] = useState<{ status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; text: string }>({ status: 'loading' })
  const [raw, setRaw] = useState(!markdown)
  const [wrap, setWrap] = useState(false)
  const target = useRef<HTMLSpanElement>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-reads on Reload
  useEffect(() => {
    let live = true
    setState({ status: 'loading' })
    fileApi.text(path).then(
      (r) => live && setState({ status: 'ready', text: r.text }),
      (e) => live && setState({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [path, tick])
  useEffect(() => {
    if (state.status === 'ready') target.current?.scrollIntoView?.({ block: 'center' })
  }, [state.status])

  if (state.status === 'loading') return <Skeleton className="h-40 w-full" />
  if (state.status === 'error') return <p role="alert" className="text-control text-fg-secondary">{state.message}</p>
  const lines = state.text.split('\n')
  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        {markdown && (
          <button type="button" className={BTN} aria-pressed={raw} onClick={() => setRaw((r) => !r)}>
            {raw ? 'Rendered' : 'Raw'}
          </button>
        )}
        {raw && (
          <button type="button" className={BTN} aria-pressed={wrap} onClick={() => setWrap((w) => !w)}>
            Wrap
          </button>
        )}
      </div>
      {markdown && !raw ? (
        <div className="prose-sm max-w-none text-control text-fg">
          <Markdown>{state.text}</Markdown>
        </div>
      ) : (
        <pre className={`max-h-[60vh] overflow-auto border border-border bg-surface p-2 font-mono text-caption leading-relaxed text-fg ${wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : ''}`}>
          {lines.map((l, i) => (
            <span key={i} ref={line === i + 1 ? target : undefined} className={`flex ${line === i + 1 ? 'bg-surface-hover' : ''}`}>
              <span aria-hidden className="w-10 shrink-0 select-none pr-2 text-right text-fg-faint">{i + 1}</span>
              <span className="min-w-0 flex-1">{l || ' '}</span>
            </span>
          ))}
        </pre>
      )}
    </div>
  )
}
