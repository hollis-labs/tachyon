import { Button, Popover, PopoverContent, PopoverTrigger, Skeleton } from '@hollis-labs/design-components'
import { Ban, ChevronDown, ChevronUp, Code, FileText, File as FileIcon, Image as ImageIcon, Maximize2, Minimize2, PanelsTopLeft, RefreshCw, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import type { RefDetailProps } from '../autolink/extensions'
import { relativeTime } from '../builder/changes'
import { Segmented } from '../components/segmented'
import { contentUrl, denyMessage, errorMessage, type FileKind, type FileStat, fileApi, formatSize, type Frame, PREVIEW_HEAD_BYTES } from './api'
import { FileAccessDialog } from './FileAccessDialog'

const TEXT_KINDS: readonly FileKind[] = ['text', 'json', 'yaml', 'toml', 'csv', 'code']
const KIND_WORD: Record<string, string> = { markdown: 'Markdown', html: 'HTML', image: 'Image', json: 'JSON', yaml: 'YAML', toml: 'TOML', csv: 'CSV', code: 'Code', text: 'Text', binary: 'Binary' }
const LANG: Record<string, string> = { go: 'Go', ts: 'TypeScript', tsx: 'TSX', js: 'JavaScript', py: 'Python', rs: 'Rust', sh: 'Shell', sql: 'SQL', css: 'CSS', json: 'JSON', yaml: 'YAML', toml: 'TOML', md: 'Markdown' }
const KIND_ICON: Record<string, typeof FileIcon> = { html: PanelsTopLeft, markdown: FileText, code: Code, image: ImageIcon, text: FileText, json: Code, yaml: Code, toml: Code, csv: FileText }

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; stat: FileStat }

const CHIP = 'rounded-xs border border-border px-1.5 py-0.5 font-mono text-caption text-fg-secondary'
const HEAD = 'text-control font-bold text-fg'
const MUTED = 'text-control text-fg-muted'

const basename = (p: string) => p.split('/').pop() ?? p
const extOf = (p: string) => basename(p).split('.').pop()?.toLowerCase() ?? ''

const ICON_BY_EXT: Record<string, string> = {
  html: 'file-html', htm: 'file-html', png: 'file-image', jpg: 'file-image', jpeg: 'file-image', gif: 'file-image', webp: 'file-image', svg: 'file-image',
  json: 'file-json', yaml: 'file-json', yml: 'file-json', toml: 'file-json', csv: 'file-json',
  md: 'file', txt: 'file', log: 'file',
}
/** Icon token for a file chip, from the extension alone (no stat on a chip render). */
export const chipIconToken = (path: string) => ICON_BY_EXT[extOf(path)] ?? (extOf(path) === basename(path) ? 'file' : 'file-code')

export function metaLine(stat: FileStat | null, path: string): string {
  if (!stat) return ''
  if (!stat.exists || !stat.allowed) return stat.deny_reason === 'too_large' ? ['Large', formatSize(stat.size)].filter(Boolean).join(' · ') : 'Blocked'
  const kind = stat.kind ?? 'binary'
  const word = kind === 'code' ? (LANG[stat.language ?? extOf(path)] ?? 'Code') : (KIND_WORD[kind] ?? kind)
  const when = stat.mtime ? `modified ${relativeTime(new Date(stat.mtime).getTime())}` : ''
  return [word, formatSize(stat.size), when].filter(Boolean).join(' · ')
}

/** Drawer for a file path: header and actions, the viewer that fits the kind, the path footer. */
export function FileViewerPane({ refId, onClose, expanded = false, onExpandedChange }: RefDetailProps) {
  const path = refId.canonical
  const line = typeof refId.meta?.line === 'number' ? refId.meta.line : undefined
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [tick, setTick] = useState(0)
  const reload = useCallback(() => setTick((n) => n + 1), [])
  const [copied, setCopied] = useState(false)
  const [changed, setChanged] = useState(false)
  const [access, setAccess] = useState(false)
  const [text, setText] = useState<string | null>(null)
  const [dims, setDims] = useState('')
  const stat = load.status === 'ready' ? load.stat : null
  const canOpen = !!stat && stat.exists && stat.allowed && stat.type !== 'dir'
  const kind = stat?.kind ?? 'binary'
  const name = basename(path)

  const copy = (value: string) => {
    void navigator.clipboard?.writeText(value)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-runs the stat on Reload
  useEffect(() => {
    let live = true
    setChanged(false)
    setText(null)
    setDims('')
    setLoad({ status: 'loading' })
    fileApi.stat(path).then(
      (s) => live && setLoad({ status: 'ready', stat: s }),
      (e) => live && setLoad({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [path, tick])

  // Every 30s while visible, ask again; a different etag, size or mtime means the file moved on. Never auto-reloads.
  const seen = stat ? `${stat.etag ?? ''}|${stat.size ?? ''}|${stat.mtime ?? ''}` : null
  useEffect(() => {
    if (!seen || !canOpen) return
    const t = setInterval(() => {
      if (document.hidden) return
      fileApi.stat(path).then(
        (s) => {
          if (`${s.etag ?? ''}|${s.size ?? ''}|${s.mtime ?? ''}` !== seen) setChanged(true)
        },
        () => undefined,
      )
    }, 30000)
    return () => clearInterval(t)
  }, [seen, canOpen, path])

  const blocked = !!stat && (!stat.exists || !stat.allowed)
  const large = blocked && stat?.deny_reason === 'too_large'
  const Icon = blocked ? Ban : (KIND_ICON[kind] ?? FileIcon)
  const textual = canOpen && (kind === 'markdown' || TEXT_KINDS.includes(kind))
  const meta = kind === 'image' && dims ? `${(stat?.language ?? extOf(path)).toUpperCase()} · ${dims} · ${formatSize(stat?.size)}` : metaLine(stat, path)
  const sm = 'h-7 text-label'

  return (
    // Ctrl/Cmd+F opens our find only while focus is inside the viewer and a find box exists (raw text).
    <section
      aria-label={`File ${name}`}
      className="flex min-h-0 flex-1 flex-col"
      onKeyDown={(e) => {
        if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== 'f') return
        const box = e.currentTarget.querySelector<HTMLInputElement>('input[aria-label="Find in file"]:not(:disabled)')
        if (!box) return
        e.preventDefault()
        box.focus()
        box.select()
      }}
    >
      <header className="flex flex-wrap items-start gap-x-3 gap-y-2 border-b border-border-subtle px-4 py-3">
        <span aria-hidden className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-control bg-surface text-fg-secondary">
          <Icon className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1 basis-40">
          <h2 className="truncate font-mono text-control font-bold normal-case tracking-normal text-fg" title={path}>
            {name}
            {line ? `:${line}` : ''}
          </h2>
          <p className="text-label text-fg-muted">{meta || ' '}</p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-1.5">
          {canOpen && onExpandedChange && (
            <Button type="button" variant="outline" size="sm" className={sm} onClick={() => onExpandedChange(!expanded)}>
              {expanded ? <Minimize2 aria-hidden className="size-3" /> : <Maximize2 aria-hidden className="size-3" />}
              {expanded ? 'Collapse' : 'Expand'}
            </Button>
          )}
          {textual && text !== null && (
            <Button type="button" variant="outline" size="sm" className={sm} onClick={() => copy(text)}>
              {copied ? 'Copied' : 'Copy'}
            </Button>
          )}
          {(canOpen || large) && (
            <Button type="button" variant="outline" size="sm" className={sm} render={<a href={contentUrl(path)} download={name} />}>
              Download
            </Button>
          )}
          {(!textual || text === null) && kind !== 'html' && (
            <Button type="button" variant="outline" size="sm" className={sm} onClick={() => copy(path)}>
              {copied ? 'Copied' : 'Copy path'}
            </Button>
          )}
          {canOpen && kind !== 'html' && kind !== 'markdown' && !TEXT_KINDS.includes(kind) && (
            <Button type="button" variant="outline" size="sm" className={sm} render={<a href={contentUrl(path)} target="_blank" rel="noopener noreferrer" />}>
              Open in tab ↗
            </Button>
          )}
          {canOpen && kind === 'html' && (
            <Button type="button" variant="outline" size="sm" className={sm} render={<a href={contentUrl(path)} target="_blank" rel="noopener noreferrer" />}>
              Open in tab ↗
            </Button>
          )}
          {kind !== 'image' && (
            <Button type="button" variant="outline" size="sm" className={sm} aria-label="Reload" onClick={reload}>
              <RefreshCw aria-hidden className="size-3" />
            </Button>
          )}
          {onClose && (
            <Button type="button" variant="outline" size="sm" className={sm} aria-label="Close" onClick={onClose}>
              <X aria-hidden className="size-3" />
            </Button>
          )}
        </div>
      </header>
      <div tabIndex={0} className="min-h-0 flex-1 space-y-3 overflow-auto px-4 py-3">
        {changed && (
          <p role="status" className="flex items-center gap-2 text-label text-fg-muted">
            Changed on disk.
            <Button type="button" variant="link" size="xs" onClick={reload}>
              Reload
            </Button>
          </p>
        )}
        {load.status === 'loading' && (
          <div role="status" aria-busy="true" aria-label="Loading file" className="space-y-2">
            <Skeleton className="h-4 w-1/3" />
            <Skeleton className="h-40 w-full" />
          </div>
        )}
        {load.status === 'error' && (
          <div role="alert" className="space-y-2 pt-3">
            <p className={HEAD}>Could not open this file</p>
            <p className={MUTED}>{load.message}</p>
            <Button type="button" variant="outline" size="sm" className={sm} onClick={reload}>
              Try again
            </Button>
          </div>
        )}
        {load.status === 'ready' && (
          <Body stat={load.stat} path={path} line={line} tick={tick} expanded={expanded} onText={setText} onDims={setDims} onAccess={() => setAccess(true)} onCopyPath={() => copy(path)} />
        )}
      </div>
      <footer className="truncate border-t border-border-subtle px-4 py-2.5 font-mono text-caption text-fg-muted" title={path}>
        {path}
      </footer>
      <FileAccessDialog open={access} onOpenChange={setAccess} />
    </section>
  )
}

interface BodyProps {
  stat: FileStat
  path: string
  line?: number
  tick: number
  expanded?: boolean
  onText?: (text: string) => void
  onDims?: (dims: string) => void
  onAccess?: () => void
  onCopyPath?: () => void
}

export function Body({ stat, path, line, tick, expanded = false, onText, onDims, onAccess, onCopyPath }: BodyProps) {
  if (!stat.exists || !stat.allowed) {
    if (stat.deny_reason === 'too_large') {
      const kindOfBig = stat.kind ?? 'binary'
      if (kindOfBig === 'markdown' || TEXT_KINDS.includes(kindOfBig)) {
        return (
          <div className="space-y-2 pt-3">
            <p className={HEAD}>File is {formatSize(stat.size)}, showing the first 200 KB</p>
            <TextBody path={path} head tick={tick} expanded={expanded} language={stat.language ?? extOf(path)} onText={onText} />
          </div>
        )
      }
      return (
        <div className="space-y-1 pt-3">
          <p className={HEAD}>File is {formatSize(stat.size)}, too large to preview</p>
          <p className={MUTED}>Previews are limited to 5 MB for text and 20 MB for images. Download the file to read it.</p>
        </div>
      )
    }
    const generic = !stat.exists || stat.deny_reason === 'not_found_or_hidden' || !stat.deny_reason
    return (
      <div className="space-y-2 pt-3">
        <p className={HEAD}>{generic ? 'Not found or not available to view' : 'Not available to view'}</p>
        <p className={MUTED}>{denyMessage(stat.deny_reason, stat.exists)}</p>
        <p className="font-mono text-control text-fg-secondary">{path}</p>
        <div className="flex gap-2">
          {onAccess && (
            <Button type="button" variant="outline" size="sm" className="h-7 text-label" onClick={onAccess}>
              File access settings
            </Button>
          )}
          {onCopyPath && (
            <Button type="button" variant="outline" size="sm" className="h-7 text-label" onClick={onCopyPath}>
              Copy path
            </Button>
          )}
        </div>
      </div>
    )
  }
  if (stat.type === 'dir') return <p className={MUTED}>This is a folder. Folders are not listed.</p>
  const kind = stat.kind ?? 'binary'
  if (kind === 'image') return <Picture path={path} ext={stat.language ?? extOf(path)} size={stat.size} onDims={onDims} />
  if (kind === 'html') return <Page path={path} tick={tick} expanded={expanded} onText={onText} />
  if (kind === 'markdown') return <TextBody path={path} markdown tick={tick} expanded={expanded} onText={onText} />
  if (TEXT_KINDS.includes(kind)) return <TextBody path={path} line={line} tick={tick} expanded={expanded} language={stat.language ?? extOf(path)} onText={onText} />
  return <p className={MUTED}>No preview for this type. Copy the path to open it elsewhere.</p>
}

function Picture({ path, ext, size, onDims }: { path: string; ext: string; size?: number | null; onDims?: (d: string) => void }) {
  const [fit, setFit] = useState<'fit' | 'full'>('fit')
  const [broken, setBroken] = useState(false)
  const [dims, setDims] = useState('')
  if (broken) {
    return (
      <p role="alert" className={MUTED}>
        The image could not be loaded.
      </p>
    )
  }
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <span className={CHIP}>{ext}</span>
        <span className="text-label text-fg-muted">{[dims, formatSize(size)].filter(Boolean).join(' · ')}</span>
        <span className="flex-1" />
        <Segmented<'fit' | 'full'>
          bare
          label="Image size"
          value={fit}
          onChange={setFit}
          items={[
            { value: 'fit', label: 'Fit' },
            { value: 'full', label: '100%' },
          ]}
        />
      </div>
      {/* KIT-GAP: the checkerboard is drawn from tokens here until the kit has a transparency ground. */}
      <div tabIndex={0} className="max-h-[70vh] overflow-auto rounded-panel border border-border bg-[repeating-conic-gradient(var(--color-surface)_0_25%,var(--color-bg)_0_50%)] bg-[length:12px_12px] p-2">
        {/* SVG is only ever shown through <img>, as the contract requires. */}
        <img
          src={contentUrl(path)}
          alt={basename(path)}
          onLoad={(e) => {
            const d = `${e.currentTarget.naturalWidth} × ${e.currentTarget.naturalHeight}`
            setDims(d)
            onDims?.(d)
          }}
          onError={() => setBroken(true)}
          className={fit === 'fit' ? 'mx-auto h-auto max-w-full' : 'h-auto max-w-none'}
        />
      </div>
    </div>
  )
}

/** HTML renders in a single-use sandboxed frame (no scripts, no same-origin); Source shows the text. */
function Page({ path, tick, expanded, onText }: { path: string; tick: number; expanded: boolean; onText?: (t: string) => void }) {
  const [view, setView] = useState<'rendered' | 'source'>('rendered')
  const [width, setWidth] = useState<'desktop' | 'mobile'>('desktop')
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
      <div className="flex flex-wrap items-center gap-2">
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
        {view === 'rendered' && (
          <Segmented<'desktop' | 'mobile'>
            bare
            label="Preview width"
            value={width}
            onChange={setWidth}
            items={[
              { value: 'desktop', label: 'Desktop 1440' },
              { value: 'mobile', label: 'Mobile 390' },
            ]}
          />
        )}
      </div>
      {view === 'source' && <TextBody path={path} tick={tick} expanded={expanded} language="html" onText={onText} />}
      {view === 'rendered' && frame === null && <Skeleton className="h-64 w-full" />}
      {view === 'rendered' && frame && 'error' in frame && (
        <p role="alert" className={MUTED}>
          {frame.error} Try Source.
        </p>
      )}
      {view === 'rendered' && frame && 'frame_url' in frame && (
        <div tabIndex={0} className="overflow-auto rounded-panel border border-border p-2">
          <iframe
            title={`Preview of ${basename(path)}`}
            src={frame.frame_url}
            sandbox=""
            referrerPolicy="no-referrer"
            className={`${expanded ? 'h-[70vh]' : 'h-96'} ${width === 'mobile' ? 'mx-auto w-[390px] max-w-full' : 'w-full'} rounded-control border border-border-subtle bg-white`}
          />
        </div>
      )}
      {view === 'rendered' && <p className="text-label text-fg-muted">Scripts, network, navigation and external files are blocked; the page must be self-contained.</p>}
    </div>
  )
}

function TextBody({ path, line, markdown, head, tick, expanded = false, language, onText }: { path: string; line?: number; markdown?: boolean; head?: boolean; tick: number; expanded?: boolean; language?: string; onText?: (t: string) => void }) {
  const [state, setState] = useState<{ status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; text: string }>({ status: 'loading' })
  const [raw, setRaw] = useState(!markdown)
  const [wrap, setWrap] = useState(false)
  const target = useRef<HTMLSpanElement>(null)
  const [find, setFind] = useState('')
  const [at, setAt] = useState(0)
  const rows = useRef(new Map<number, HTMLSpanElement>())
  const rendered = useRef<HTMLDivElement>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-reads on Reload
  useEffect(() => {
    let live = true
    setState({ status: 'loading' })
    const read = head ? fileApi.textHead(path, PREVIEW_HEAD_BYTES) : fileApi.text(path)
    read.then(
      (r) => {
        if (!live) return
        setState({ status: 'ready', text: r.text })
        onText?.(r.text)
      },
      (e) => live && setState({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [path, tick, head])
  useEffect(() => {
    if (state.status === 'ready') target.current?.scrollIntoView?.({ block: 'center' })
  }, [state.status])

  if (state.status === 'loading') return <Skeleton className="h-40 w-full" />
  if (state.status === 'error') {
    return (
      <p role="alert" className={MUTED}>
        {state.message}
      </p>
    )
  }
  const lines = state.text.split('\n')
  const needle = find.trim().toLowerCase()
  const hits = needle ? lines.flatMap((l, i) => (l.toLowerCase().includes(needle) ? [i] : [])) : []
  const hitSet = new Set(hits)
  const current = hits.length ? hits[at % hits.length] : -1
  const step = (d: number) => {
    if (!hits.length) return
    const n = (at + d + hits.length) % hits.length
    setAt(n)
    rows.current.get(hits[n])?.scrollIntoView?.({ block: 'center' })
  }
  const headings = markdown ? lines.flatMap((l, i) => (/^#{1,3}\s+\S/.test(l) ? [{ i, text: l.replace(/^#+\s+/, ''), depth: l.match(/^#+/)?.[0].length ?? 1 }] : [])) : []
  const jump = (n: number, line: number) => {
    if (!raw) rendered.current?.querySelectorAll('h1,h2,h3')[n]?.scrollIntoView?.({ block: 'start' })
    else rows.current.get(line)?.scrollIntoView?.({ block: 'start' })
  }
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {markdown ? (
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
        ) : (
          <>
            {language && <span className={CHIP}>{language}</span>}
            <span className="text-label text-fg-muted">{lines.length} lines · UTF-8</span>
          </>
        )}
        <span className="flex-1" />
        {markdown && headings.length > 0 && (
          <Popover>
            <PopoverTrigger className="inline-flex h-7 items-center gap-1 rounded-control px-2 text-label text-fg-muted hover:text-fg">
              Outline <ChevronDown aria-hidden className="size-3" />
            </PopoverTrigger>
            <PopoverContent align="end" className="w-64 p-1">
              <ul aria-label="Outline">
                {headings.map((h, n) => (
                  <li key={h.i}>
                    <button type="button" className="block w-full truncate rounded-xs px-2 py-1 text-left text-control text-fg-secondary hover:bg-selection" style={{ paddingLeft: `${0.5 + (h.depth - 1) * 0.75}rem` }} onClick={() => jump(n, h.i)}>
                      {h.text}
                    </button>
                  </li>
                ))}
              </ul>
            </PopoverContent>
          </Popover>
        )}
        {raw && (
          <div className="flex items-center gap-1">
            <input
              type="search"
              aria-label="Find in file"
              placeholder="Find in file"
              value={find}
              onChange={(e) => {
                setFind(e.target.value)
                setAt(0)
              }}
              onKeyDown={(e) => e.key === 'Enter' && step(e.shiftKey ? -1 : 1)}
              className="h-7 w-36 rounded-control border border-border bg-transparent px-2 text-label text-fg placeholder:text-fg-muted"
            />
            {needle && (
              <span className="text-caption text-fg-muted" aria-live="polite">
                {hits.length ? `${(at % hits.length) + 1} of ${hits.length}` : 'No matches'}
              </span>
            )}
            {needle && (
              <>
                <Button type="button" variant="outline" size="sm" className="h-7" aria-label="Previous match" disabled={!hits.length} onClick={() => step(-1)}>
                  <ChevronUp aria-hidden className="size-3" />
                </Button>
                <Button type="button" variant="outline" size="sm" className="h-7" aria-label="Next match" disabled={!hits.length} onClick={() => step(1)}>
                  <ChevronDown aria-hidden className="size-3" />
                </Button>
              </>
            )}
            <Button type="button" variant="outline" size="sm" className="h-7 text-label" aria-pressed={wrap} onClick={() => setWrap((w) => !w)}>
              Wrap
            </Button>
          </div>
        )}
      </div>
      {markdown && !raw ? (
        <div ref={rendered} className="max-w-none text-control text-fg [&_:is(h1,h2,h3,h4)]:font-sans [&_:is(h1,h2,h3,h4)]:normal-case [&_:is(h1,h2,h3,h4)]:tracking-normal [&_:is(h1,h2,h3,h4)]:font-bold [&_:is(h1,h2,h3,h4)]:text-fg [&_h1]:mb-2 [&_h1]:text-lg [&_h2]:mb-1 [&_h2]:mt-3 [&_h2]:text-control [&_p]:my-2 [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_code]:font-mono [&_code]:text-label [&_pre]:overflow-auto [&_pre]:rounded-control [&_pre]:border [&_pre]:border-border-subtle [&_pre]:p-2">
          <Markdown>{state.text}</Markdown>
        </div>
      ) : (
        <pre tabIndex={0} className={`${expanded ? 'max-h-[75vh]' : 'max-h-[60vh]'} overflow-auto rounded-panel border border-border-subtle bg-bg py-2 font-mono text-caption leading-relaxed text-fg ${wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : ''}`}>
          {lines.map((l, i) => (
            <span
              key={i}
              ref={(el) => {
                if (el) rows.current.set(i, el)
                else rows.current.delete(i)
                if (line === i + 1) target.current = el
              }}
              className={`flex ${i === current ? 'bg-selection' : hitSet.has(i) ? 'bg-surface' : line === i + 1 ? 'bg-selection font-bold' : ''}`}
            >
              <span aria-hidden className="w-12 shrink-0 select-none pr-3 text-right text-fg-muted">
                {i + 1}
              </span>
              <span className="min-w-0 flex-1 pr-3">{l || ' '}</span>
            </span>
          ))}
        </pre>
      )}
    </div>
  )
}
