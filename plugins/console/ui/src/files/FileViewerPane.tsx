import { Dialog, DialogContent, DialogDescription, DialogTitle, Popover, PopoverContent, PopoverTrigger, Skeleton } from '@hollis-labs/design-components'
import { Check, Copy, Download, ExternalLink, Maximize2, MoreHorizontal, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import type { RefDetailProps } from '../autolink/extensions'
import { Segmented } from '../components/segmented'
import { relativeTime } from '../builder/changes'
import { contentUrl, denyMessage, errorMessage, type FileKind, type FileStat, fileApi, formatSize, type Frame } from './api'

const BTN =
  'inline-flex items-center gap-1.5 border border-border px-2 py-1 text-control text-fg-secondary transition-colors hover:border-fg-faint hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'
const ITEM = 'flex min-h-10 w-full items-center gap-2 px-2 text-left text-control text-fg-secondary hover:bg-surface-hover'
const TEXT_KINDS: readonly FileKind[] = ['text', 'json', 'yaml', 'toml', 'csv', 'code']

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; stat: FileStat }

/** Drawer body for a file path: stat first, then the viewer that fits the kind. */
export function FileViewerPane({ refId }: RefDetailProps) {
  const path = refId.canonical
  const line = typeof refId.meta?.line === 'number' ? refId.meta.line : undefined
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [tick, setTick] = useState(0)
  const reload = useCallback(() => setTick((n) => n + 1), [])
  const [copied, setCopied] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const copyPath = () => {
    void navigator.clipboard?.writeText(path)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  const stat = load.status === 'ready' ? load.stat : null
  const canOpen = !!stat && stat.exists && stat.allowed && stat.type !== 'dir'
  const kindOf = stat?.kind ?? 'binary'

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
        <div className="hidden items-center gap-2 sm:flex">
          <button type="button" className={BTN} onClick={copyPath}>
            {copied ? <Check className="h-3 w-3" aria-hidden /> : <Copy className="h-3 w-3" aria-hidden />} {copied ? 'Copied' : 'Copy path'}
          </button>
          {canOpen && (
            <>
              <button type="button" className={BTN} onClick={() => setExpanded(true)}>
                <Maximize2 className="h-3 w-3" aria-hidden /> Expand
              </button>
              {kindOf !== 'html' && (
                <a className={BTN} href={contentUrl(path)} target="_blank" rel="noopener noreferrer">
                  <ExternalLink className="h-3 w-3" aria-hidden /> Open in tab
                </a>
              )}
              <a className={BTN} href={contentUrl(path)} download={path.split('/').pop()}>
                <Download className="h-3 w-3" aria-hidden /> Download
              </a>
            </>
          )}
        </div>
        <Popover>
          <PopoverTrigger aria-label="More actions" className={`${BTN} min-h-10 sm:hidden`}>
            <MoreHorizontal className="h-4 w-4" aria-hidden /> More
          </PopoverTrigger>
          <PopoverContent align="end" className="w-56 p-1">
            <div role="menu" aria-label="File actions">
              <button type="button" role="menuitem" className={ITEM} onClick={copyPath}>
                {copied ? <Check className="h-4 w-4" aria-hidden /> : <Copy className="h-4 w-4" aria-hidden />} {copied ? 'Copied' : 'Copy path'}
              </button>
              {canOpen && (
                <>
                  <button type="button" role="menuitem" className={ITEM} onClick={() => setExpanded(true)}>
                    <Maximize2 className="h-4 w-4" aria-hidden /> Expand
                  </button>
                  {kindOf !== 'html' && (
                    <a role="menuitem" className={ITEM} href={contentUrl(path)} target="_blank" rel="noopener noreferrer">
                      <ExternalLink className="h-4 w-4" aria-hidden /> Open in tab
                    </a>
                  )}
                  <a role="menuitem" className={ITEM} href={contentUrl(path)} download={path.split('/').pop()}>
                    <Download className="h-4 w-4" aria-hidden /> Download
                  </a>
                </>
              )}
            </div>
          </PopoverContent>
        </Popover>
      </div>
      {load.status === 'loading' && <Skeleton className="h-40 w-full" />}
      {load.status === 'error' && (
        <p role="alert" className="text-control text-fg-secondary">
          {load.message}
        </p>
      )}
      {load.status === 'ready' && <Body stat={load.stat} path={path} line={line} tick={tick} />}
      {load.status === 'ready' && (
        <Dialog open={expanded} onOpenChange={setExpanded}>
          <DialogContent className="max-h-[92vh] w-[min(96vw,72rem)] max-w-none overflow-auto">
            <DialogTitle className="truncate text-control">{path.split('/').pop()}</DialogTitle>
            <DialogDescription className="sr-only">Expanded view of {path}</DialogDescription>
            {expanded && <Body stat={load.stat} path={path} line={line} tick={tick} expanded />}
          </DialogContent>
        </Dialog>
      )}
      <p className="break-all font-mono text-caption text-fg-faint">{path}</p>
    </section>
  )
}

const KIND_WORD: Record<string, string> = { markdown: 'Markdown', html: 'HTML', image: 'Image', json: 'JSON', yaml: 'YAML', toml: 'TOML', csv: 'CSV', code: 'Code', text: 'Text', binary: 'Binary' }

function Body({ stat, path, line, tick, expanded = false }: { stat: FileStat; path: string; line?: number; tick: number; expanded?: boolean }) {
  if (!stat.exists || !stat.allowed) {
    return <p className="text-control text-fg-secondary">{denyMessage(stat.deny_reason, stat.exists)}</p>
  }
  if (stat.type === 'dir') return <p className="text-control text-fg-secondary">This is a folder. Folders are not listed.</p>
  const kind = stat.kind ?? 'binary'
  return (
    <>
      <p className="text-caption text-fg-muted" title={stat.mtime ? new Date(stat.mtime).toLocaleString() : undefined}>
        {[KIND_WORD[kind] ?? kind, formatSize(stat.size), stat.mtime ? relativeTime(new Date(stat.mtime).getTime()) : ''].filter(Boolean).join(', ')}
      </p>
      {kind === 'image' && <Picture path={path} />}
      {kind === 'html' && <Page path={path} tick={tick} expanded={expanded} />}
      {kind === 'markdown' && <TextBody path={path} markdown tick={tick} expanded={expanded} />}
      {TEXT_KINDS.includes(kind) && <TextBody path={path} line={line} tick={tick} expanded={expanded} />}
      {kind === 'binary' && <p className="text-control text-fg-secondary">No preview for this type. Copy the path to open it elsewhere.</p>}
    </>
  )
}

function Picture({ path }: { path: string }) {
  const [full, setFull] = useState(false)
  const [broken, setBroken] = useState(false)
  if (broken)
    return (
      <p role="alert" className="text-control text-fg-secondary">
        The image could not be loaded.
      </p>
    )
  // SVG is only ever shown through <img>, as the contract requires.
  return (
    <button
      type="button"
      className={`block w-full overflow-auto border border-border bg-surface ${full ? '' : 'cursor-zoom-in'}`}
      aria-label={full ? 'Fit image to width' : 'Show image at full size'}
      onClick={() => setFull((f) => !f)}
    >
      <img src={contentUrl(path)} alt={path.split('/').pop() ?? 'image'} onError={() => setBroken(true)} className={full ? 'max-w-none' : 'h-auto w-full'} />
    </button>
  )
}

/** HTML renders in a single-use sandboxed frame (no scripts, no same-origin); Source shows the text. */
function Page({ path, tick, expanded = false }: { path: string; tick: number; expanded?: boolean }) {
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
      <Segmented<'rendered' | 'source'>
        bare
        label="HTML view"
        value={view}
        onChange={setView}
        items={[
          { value: 'rendered', label: 'Rendered' },
          { value: 'source', label: 'Source' },
        ]}
      />
      {view === 'source' && <TextBody path={path} tick={tick} expanded={expanded} />}
      {view === 'rendered' && frame === null && <Skeleton className="h-64 w-full" />}
      {view === 'rendered' && frame && 'error' in frame && (
        <p role="alert" className="text-control text-fg-secondary">
          {frame.error} Try Source.
        </p>
      )}
      {view === 'rendered' && frame && 'frame_url' in frame && (
        <iframe
          title={`Preview of ${path.split('/').pop()}`}
          src={frame.frame_url}
          sandbox=""
          referrerPolicy="no-referrer"
          className={`${expanded ? 'h-[70vh]' : 'h-96'} w-full border border-border bg-white`}
        />
      )}
    </div>
  )
}

function TextBody({ path, line, markdown, tick, expanded = false }: { path: string; line?: number; markdown?: boolean; tick: number; expanded?: boolean }) {
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
  if (state.status === 'error')
    return (
      <p role="alert" className="text-control text-fg-secondary">
        {state.message}
      </p>
    )
  const lines = state.text.split('\n')
  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        {markdown && (
          <Segmented<'rendered' | 'raw'>
            bare
            label="Markdown view"
            value={raw ? 'raw' : 'rendered'}
            onChange={(v) => setRaw(v === 'raw')}
            items={[
              { value: 'rendered', label: 'Rendered' },
              { value: 'raw', label: 'Raw' },
            ]}
          />
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
        <pre
          className={`${expanded ? 'max-h-[75vh]' : 'max-h-[60vh]'} overflow-auto border border-border bg-surface p-2 font-mono text-caption leading-relaxed text-fg ${wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : ''}`}
        >
          {lines.map((l, i) => (
            <span key={i} ref={line === i + 1 ? target : undefined} className={`flex ${line === i + 1 ? 'bg-surface-hover' : ''}`}>
              <span aria-hidden className="w-10 shrink-0 select-none pr-2 text-right text-fg-faint">
                {i + 1}
              </span>
              <span className="min-w-0 flex-1">{l || ' '}</span>
            </span>
          ))}
        </pre>
      )}
    </div>
  )
}
