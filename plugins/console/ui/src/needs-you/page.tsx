import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, CircleHelp, Mail, MessageSquare, RefreshCw, ShieldCheck, UserRound } from 'lucide-react'
import { Button, Skeleton } from '@hollis-labs/design-components'
import { Chip } from '@planner/shared'
import { RefChip } from '../autolink'
import { consoleJson, type FetchLike } from '../lib/http'

/** GET needs-you (internal/needsyou): what waits on @chrispian, read live. */
export interface NeedsYouItem {
  id: string
  source: 'ask' | 'mail' | 'status' | 'chat' | 'approvals'
  kind: string
  why: 'reply' | 'read' | 'unblock' | 'decide'
  title: string
  from: string
  ref?: string
  since: string
  critical: boolean
  message_id?: string
  /** Counts, for an item that stands for several (a chat thread, the approvals queue). */
  detail?: string
  /** Where in Tachyon to act on it. */
  link?: string
}
export interface NeedsYouResult {
  items: NeedsYouItem[]
  sources: Record<string, { ok: boolean; error?: string; truncated?: boolean }>
  checked_at: string
}

export const NEEDS_YOU = {
  title: 'Needs you',
  intro: 'Asks waiting on your reply in chat and mail, approvals to decide, mail marked for you, and seats blocked on you. Each item leaves the list when its condition ends.',
  empty: 'Nothing is waiting on you.',
  refresh: 'Refresh',
  loadFailed: 'Could not load the needs-you list',
  checked: (t: string) => `Checked ${t}`,
  why: { reply: 'Waiting on your reply', read: 'Marked for you, unread', unblock: 'Blocked on you', decide: 'Waiting on your decision' } as Record<string, string>,
  critical: 'Critical',
  source: { asks: 'typed asks', mail: 'your mailbox', statuses: 'seat statuses', chat: 'chat threads', approvals: 'the approvals queue' } as Record<string, string>,
  incomplete: (names: string[]) => `This list may be incomplete: could not read ${names.join(', ')}.`,
  truncated: (names: string[]) => `Only the newest items of ${names.join(', ')} were checked.`,
  kind: (k: string) => k.replace(/[_-]/g, ' '),
} as const

const ICON = { ask: CircleHelp, mail: Mail, status: UserRound, chat: MessageSquare, approvals: ShieldCheck } as const
const POLL_MS = 30_000

const age = (since: string, now: number) => {
  const m = Math.max(0, Math.round((now - Date.parse(since)) / 60_000))
  return m < 60 ? `${m}m` : m < 48 * 60 ? `${Math.round(m / 60)}h` : `${Math.round(m / 1440)}d`
}

function Ref({ r }: { r: string }) {
  if (r.startsWith('task:')) return <RefChip kind="torque.task" canonical={r.slice(5)} raw={r.slice(5)} resolve="hover" />
  if (r.startsWith('https://')) return <a href={r} target="_blank" rel="noreferrer" className="font-mono text-caption text-fg-secondary underline">{r.replace(/^https:\/\//, '').slice(0, 48)}</a>
  return <span className="font-mono text-caption text-fg-muted">{r}</span>
}

export function NeedsYouList({ data, now }: { data: NeedsYouResult; now: number }) {
  const failed = Object.entries(data.sources).filter(([, s]) => !s.ok || s.error).map(([k]) => NEEDS_YOU.source[k] ?? k)
  const truncated = Object.entries(data.sources).filter(([, s]) => s.truncated).map(([k]) => NEEDS_YOU.source[k] ?? k)
  return (
    <div className="flex flex-col gap-3">
      {failed.length > 0 && <p role="alert" className="flex items-center gap-2 text-label text-warning"><AlertTriangle className="size-3.5" />{NEEDS_YOU.incomplete(failed)}</p>}
      {truncated.length > 0 && <p className="text-label text-fg-muted">{NEEDS_YOU.truncated(truncated)}</p>}
      {data.items.length === 0 ? (
        <p className="py-2 text-control text-fg-muted">{NEEDS_YOU.empty}</p>
      ) : (
        <ul aria-label={NEEDS_YOU.title} className="flex flex-col divide-y divide-border rounded-panel border border-border">
          {data.items.map((it) => {
            const Icon = ICON[it.source] ?? CircleHelp
            return (
              <li key={it.id} data-item={it.id} className="flex items-start gap-3 px-3 py-2.5">
                <Icon aria-hidden className={`mt-0.5 size-4 shrink-0 ${it.critical ? 'text-danger' : 'text-fg-secondary'}`} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    {it.critical && <Chip tone="danger">{NEEDS_YOU.critical}</Chip>}
                    {it.link ? (
                      <Link to={it.link} className="text-control font-medium text-fg underline-offset-2 hover:underline focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">{it.title || NEEDS_YOU.kind(it.kind)}</Link>
                    ) : (
                      <span className="text-control font-medium text-fg">{it.title || NEEDS_YOU.kind(it.kind)}</span>
                    )}
                  </div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-2 text-label text-fg-muted">
                    <span>{NEEDS_YOU.why[it.why] ?? it.why}</span>
                    <span>· {it.detail || NEEDS_YOU.kind(it.kind)}</span>
                    <span>· {it.from}</span>
                    {it.ref && <span>· <Ref r={it.ref} /></span>}
                  </div>
                </div>
                <span className="shrink-0 text-label text-fg-muted" title={new Date(it.since).toLocaleString()}>{age(it.since, now)}</span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

/** The needs-you page: polls the live list, newest state wins. */
export function NeedsYouPage({ fetch: f }: { fetch?: FetchLike } = {}) {
  const [data, setData] = useState<NeedsYouResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const load = useCallback(async () => {
    try {
      setData(await consoleJson<NeedsYouResult>('/needs-you', { fetch: f }))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : NEEDS_YOU.loadFailed)
    } finally {
      setNow(Date.now())
    }
  }, [f])
  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), POLL_MS)
    return () => clearInterval(t)
  }, [load])
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-4 p-6">
        <header className="flex items-start gap-3">
          <div className="flex-1">
            <h1 className="text-title font-semibold text-fg">{NEEDS_YOU.title}</h1>
            <p className="text-control text-fg-muted">{NEEDS_YOU.intro}</p>
          </div>
          <Button size="sm" variant="outline" onClick={() => void load()}><RefreshCw className="mr-1 size-3.5" />{NEEDS_YOU.refresh}</Button>
        </header>
        {error && <p role="alert" className="text-label text-danger">{NEEDS_YOU.loadFailed}: {error}</p>}
        {data === null ? (
          !error && <div aria-busy className="flex flex-col gap-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}</div>
        ) : (
          <>
            <NeedsYouList data={data} now={now} />
            <p className="text-label text-fg-muted">{NEEDS_YOU.checked(new Date(data.checked_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }))}</p>
          </>
        )}
      </div>
    </div>
  )
}
