import { memo, useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, MessageSquarePlus } from 'lucide-react'
import { Button } from '@hollis-labs/design-components'
import type { CommentSide, DiffLine, FileDiff, ForgeComment } from '../types'
import { Badge, Composer, ThreadView } from './Comments'
import {
  LARGE_FILE_LINES, ROW_CHUNK, anchorKey, fileAnchorId, diffLineCount, langOf, lineAnchor, splitRows, statusGlyph, tokenize, unifiedRows,
  type Thread, type ThreadIndex, type TokenKind,
} from './diff-utils'
import type { PendingComment, WriteResult } from './useWorkspace'

export type DiffMode = 'unified' | 'split'

const TOKEN_CLASS: Record<TokenKind, string> = {
  plain: '',
  keyword: 'text-fg-muted',
  string: 'text-success',
  comment: 'italic text-fg-faint',
  number: 'text-fg-muted',
}

const LINE_BG: Record<DiffLine['kind'], string> = {
  add: 'bg-success/10',
  del: 'bg-danger/10',
  context: '',
}
const LINE_MARK: Record<DiffLine['kind'], string> = { add: '+', del: '−', context: ' ' }
const MARK_COLOR: Record<DiffLine['kind'], string> = { add: 'text-success', del: 'text-danger', context: 'text-fg-faint' }

const Code = memo(function Code({ text, lang }: { text: string; lang: ReturnType<typeof langOf> }) {
  const tokens = useMemo(() => tokenize(text, lang), [text, lang])
  return (
    <>
      {tokens.map((t, i) => (t.kind === 'plain' ? t.text : <span key={i} className={TOKEN_CLASS[t.kind]}>{t.text}</span>))}
      {text === '' && '​'}
    </>
  )
})

interface Composing {
  anchor: string
  path: string
  line: number
  side: CommentSide
}

export interface FileDiffViewProps {
  file: FileDiff
  mode: DiffMode
  open: boolean
  onToggle: () => void
  index: ThreadIndex
  posting: ForgeComment[]
  pending: PendingComment[]
  readOnly: boolean
  forgeUrl?: string
  postComment: (input: { body: string; path?: string; line?: number; side?: CommentSide; inReplyTo?: string }) => Promise<WriteResult>
  queueComment: (c: Omit<PendingComment, 'id'>) => void
  unqueueComment: (id: string) => void
}

export function FileDiffView(props: FileDiffViewProps) {
  const { file, mode, open, onToggle, index, posting, pending, readOnly, forgeUrl } = props
  const lang = langOf(file.path)
  const total = diffLineCount(file)
  const large = total >= LARGE_FILE_LINES
  const [limit, setLimit] = useState(ROW_CHUNK)
  const [composing, setComposing] = useState<Composing | null>(null)
  const detached = index.detached.get(file.path) ?? []
  const threadCount = detached.length + [...index.byAnchor.keys()].filter((k) => k.startsWith(`${file.path}:`)).length
  const unified = useMemo(() => (open && mode === 'unified' ? unifiedRows(file) : []), [open, mode, file])
  const split = useMemo(() => (open && mode === 'split' ? splitRows(file) : []), [open, mode, file])
  const rowsTotal = mode === 'unified' ? unified.length : split.length
  const windowed = large && rowsTotal > limit
  const cap = large ? limit : Infinity

  const replyTo = (inReplyTo: string, body: string) => {
    const t = index.all.find((x) => x.id === inReplyTo)
    return props.postComment({ body, path: file.path, line: t?.root.line, side: t?.root.side ?? 'right', inReplyTo })
  }

  const annotations = (anchors: (string | null)[]): ReactNode => {
    const seen = new Set<string>()
    const nodes: ReactNode[] = []
    for (const a of anchors) {
      if (!a || seen.has(a)) continue
      seen.add(a)
      for (const t of index.byAnchor.get(a) ?? []) {
        nodes.push(<ThreadView key={t.id} thread={t} posting={posting} readOnly={readOnly} onReply={replyTo} />)
      }
      for (const q of pending.filter((p) => p.path === file.path && p.line != null && anchorKey(p.path!, p.side ?? 'right', p.line) === a)) {
        nodes.push(
          <div key={q.id} className="flex items-start gap-2 border-y border-dashed border-border bg-surface px-3 py-2" data-testid="forge-pending-comment">
            <Badge>Pending review</Badge>
            <p className="min-w-0 flex-1 whitespace-pre-wrap text-control text-fg [overflow-wrap:anywhere]">{q.body}</p>
            <button type="button" onClick={() => props.unqueueComment(q.id)} className="font-mono text-caption uppercase tracking-label text-fg-faint hover:text-danger focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
              Discard
            </button>
          </div>,
        )
      }
      for (const p of posting) {
        if (p.kind === 'review' && !p.inReplyTo && p.path === file.path && p.line != null && anchorKey(p.path, p.side ?? 'right', p.line) === a) {
          nodes.push(<div key={p.id} className="border-y border-border bg-surface"><ThreadPostingNote c={p} /></div>)
        }
      }
      if (composing?.anchor === a) {
        nodes.push(
          <div key="composer" className="border-y border-fg bg-surface px-3 py-2" data-testid="forge-inline-composer">
            <Composer
              autoFocus
              placeholder={`Comment on line ${composing.line}…`}
              submitLabel="Comment"
              secondary={{
                label: 'Add to review',
                run: (body) => {
                  props.queueComment({ body, path: composing.path, line: composing.line, side: composing.side })
                  setComposing(null)
                },
              }}
              onCancel={() => setComposing(null)}
              onSubmit={async (body) => {
                const r = await props.postComment({ body, path: composing.path, line: composing.line, side: composing.side })
                if (r.ok) setComposing(null)
                return r
              }}
            />
          </div>,
        )
      }
    }
    return nodes.length ? <div>{nodes}</div> : null
  }

  const gutterBtn = (line: DiffLine) => {
    const a = lineAnchor(file.path, line)
    if (readOnly || !a) return null
    const side: CommentSide = line.kind === 'del' ? 'left' : 'right'
    const n = (side === 'left' ? line.oldLine : line.newLine)!
    return (
      <button
        type="button"
        aria-label={`Comment on line ${n}`}
        onClick={() => setComposing({ anchor: a, path: file.path, line: n, side })}
        className="absolute inset-y-0 left-0 hidden w-4 items-center justify-center bg-fg text-bg group-hover/row:flex focus-visible:flex focus-visible:outline-none"
      >
        <MessageSquarePlus className="h-3 w-3" />
      </button>
    )
  }

  const lineCell = (line: DiffLine, which: 'old' | 'new') => {
    const n = which === 'old' ? line.oldLine : line.newLine
    return <span className="select-none px-2 text-right text-fg-faint">{n ?? ''}</span>
  }

  const body = (() => {
    if (file.binary) return <Note>Binary file not shown.</Note>
    if (file.truncated) {
      return (
        <Note>
          The forge omitted this patch because it is too large.{' '}
          {forgeUrl && <a href={forgeUrl} target="_blank" rel="noreferrer" className="text-fg-muted underline">View on the forge</a>}
        </Note>
      )
    }
    if (file.hunks.length === 0) {
      return <Note>{file.status === 'renamed' ? 'Renamed without content changes.' : file.status === 'removed' ? 'File removed.' : 'No content changes.'}</Note>
    }
    if (large && !open) return null
    let shown = 0
    return (
      <div className="font-mono text-xs leading-5" role="table" aria-label={`Diff of ${file.path}`}>
        {mode === 'unified'
          ? unified.map((row) => {
              if (row.type === 'hunk') return <HunkHeader key={row.key} text={row.header} />
              if (shown++ >= cap) return null
              const l = row.line
              const ann = annotations([lineAnchor(file.path, l)])
              return (
                <div key={row.key} className="contents">
                  <div className={`group/row relative grid grid-cols-[3rem_3rem_1.25rem_minmax(0,1fr)] ${LINE_BG[l.kind]}`} role="row">
                    {gutterBtn(l)}
                    {lineCell(l, 'old')}
                    {lineCell(l, 'new')}
                    <span className={`select-none text-center ${MARK_COLOR[l.kind]}`}>{LINE_MARK[l.kind]}</span>
                    <span className="whitespace-pre-wrap pr-3 text-fg [overflow-wrap:anywhere]"><Code text={l.text} lang={lang} /></span>
                  </div>
                  {ann}
                </div>
              )
            })
          : split.map((row) => {
              if (row.type === 'hunk') return <HunkHeader key={row.key} text={row.header} />
              if (shown++ >= cap) return null
              const la = row.left && row.left.kind === 'del' ? lineAnchor(file.path, row.left) : null
              const ra = row.right ? lineAnchor(file.path, row.right) : null
              const ann = annotations([la, ra])
              return (
                <div key={row.key} className="contents">
                  <div className="grid grid-cols-2 divide-x divide-border" role="row">
                    <SplitCell line={row.left} side="old" lang={lang} gutter={row.left && row.left.kind === 'del' ? gutterBtn(row.left) : null} />
                    <SplitCell line={row.right} side="new" lang={lang} gutter={row.right ? gutterBtn(row.right) : null} />
                  </div>
                  {ann}
                </div>
              )
            })}
        {windowed && (
          <div className="flex items-center gap-3 border-t border-border bg-surface px-3 py-2">
            <span className="font-mono text-caption text-fg-faint">Showing {Math.min(limit, rowsTotal)} of {rowsTotal} rows</span>
            <Button variant="outline" size="sm" onClick={() => setLimit((l) => l + ROW_CHUNK)}>Show {Math.min(ROW_CHUNK, rowsTotal - limit)} more</Button>
            <Button variant="outline" size="sm" onClick={() => setLimit(rowsTotal)}>Show all</Button>
          </div>
        )}
      </div>
    )
  })()

  return (
    <section id={fileAnchorId(file.path)} className="scroll-mt-14 border-b border-border [content-visibility:auto]" data-testid="forge-file" data-path={file.path}>
      <header className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-bg px-3 py-1.5">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={open}
          aria-label={`${open ? 'Collapse' : 'Expand'} ${file.path}`}
          className="flex min-w-0 flex-1 items-center gap-2 text-left focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0 text-fg-faint" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0 text-fg-faint" />}
          <span className={`w-4 shrink-0 text-center font-mono text-label ${GLYPH_COLOR[file.status] ?? 'text-fg-muted'}`} title={file.status}>{statusGlyph(file)}</span>
          <span className="min-w-0 truncate font-mono text-xs text-fg" title={file.path}>
            {file.status === 'renamed' && file.oldPath ? <><span className="text-fg-faint">{file.oldPath}</span> {'→'} </> : null}
            {file.path}
          </span>
        </button>
        {file.binary && <Badge>binary</Badge>}
        {file.truncated && <Badge>truncated</Badge>}
        {threadCount > 0 && <Badge>{threadCount} thread{threadCount === 1 ? '' : 's'}</Badge>}
        <span className="shrink-0 font-mono text-label text-success">+{file.additions}</span>
        <span className="shrink-0 font-mono text-label text-danger">{'−'}{file.deletions}</span>
      </header>
      {open && detached.length > 0 && (
        <div className="border-b border-border" data-testid="forge-detached-threads">
          <p className="px-3 py-1.5 font-mono text-caption uppercase tracking-label text-fg-faint">Comments not on a visible line</p>
          {detached.map((t: Thread) => <ThreadView key={t.id} thread={t} posting={posting} readOnly={readOnly} onReply={replyTo} />)}
        </div>
      )}
      {open ? body : large ? (
        <div className="px-3 py-2 text-xs text-fg-faint">Large diff ({total} lines) is collapsed.</div>
      ) : null}
    </section>
  )
}

const GLYPH_COLOR: Record<string, string> = { added: 'text-success', removed: 'text-danger', renamed: 'text-fg-muted', modified: 'text-fg-muted' }

function Note({ children }: { children: ReactNode }) {
  return <p className="px-4 py-3 text-xs text-fg-faint">{children}</p>
}

function HunkHeader({ text }: { text: string }) {
  return <div className="truncate border-y border-border bg-surface px-3 font-mono text-label text-fg-muted">{text}</div>
}

function SplitCell({ line, side, lang, gutter }: { line?: DiffLine; side: 'old' | 'new'; lang: ReturnType<typeof langOf>; gutter: ReactNode }) {
  if (!line) return <div className="bg-surface" aria-hidden />
  const n = side === 'old' ? line.oldLine : line.newLine
  return (
    <div className={`group/row relative grid grid-cols-[3rem_1.25rem_minmax(0,1fr)] ${LINE_BG[line.kind]}`}>
      {gutter}
      <span className="select-none px-2 text-right text-fg-faint">{n ?? ''}</span>
      <span className={`select-none text-center ${MARK_COLOR[line.kind]}`}>{LINE_MARK[line.kind]}</span>
      <span className="whitespace-pre-wrap pr-3 text-fg [overflow-wrap:anywhere]"><Code text={line.text} lang={lang} /></span>
    </div>
  )
}

function ThreadPostingNote({ c }: { c: ForgeComment }) {
  return (
    <div className="px-3 py-2 opacity-60" aria-busy>
      <span className="font-mono text-xs text-fg">@{c.author.login}</span>
      <span className="ml-2 font-mono text-caption text-fg-faint">posting…</span>
      <p className="mt-1 whitespace-pre-wrap text-control text-fg">{c.body}</p>
    </div>
  )
}
