import { type ReactNode, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { Link } from 'react-router-dom'
import { Chip } from '../components/chip'
import { type HistoryEntry, type UsedBy, builderApi, errorMessage } from './api'
import { relativeTime } from './changes'
import { Diff } from './ReviewDialog'
import { type Load, useLoad } from './useLoad'
import { Cap, Muted, RailCard, RailRow } from './parts'
import { NOT_REPORTED, SECTIONS } from './vocab'

const CHECK_WORD = { ok: 'Passed', warn: 'Warning', fail: 'Failed' } as const

const none = <span className="text-fg-faint">none</span>
const absolute = (at: number) => new Date(at > 1e12 ? at : at * 1000).toLocaleString()

function Block({ id, children }: { id: string; children: ReactNode }) {
  const label = SECTIONS.find((s) => s.id === id)?.label ?? id
  return (
    <section id={`section-${id}`} aria-labelledby={`h-${id}`} className="flex scroll-mt-4 flex-col gap-2">
      <Cap id={`h-${id}`}>{label}</Cap>
      {children}
    </section>
  )
}

function Body<T>({ load, retry, children }: { load: Load<T>; retry: () => void; children: (d: T) => ReactNode }) {
  if (load.status === 'loading') return <div aria-busy="true" className="h-12 animate-pulse rounded-control bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="flex items-center gap-3">
        <span className="text-control text-fg-secondary">{load.message}</span>
        <Button type="button" onClick={retry} variant="outline" size="sm">
          Retry
        </Button>
      </div>
    )
  return <>{children(load.data)}</>
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

/** Resolved card: what this agent will get. Tachyon's own estimate is labelled so, and unknown counts are left out. */
export function ResolvedCard({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.resolved(id), [id])
  return (
    <RailCard
      title="Resolved · what it will get"
      aside={
        load.status === 'ready' && load.data.resolved_by !== 'tether' ? (
          <Chip tone="faint">
            <span title="Tether did not compute this. Tachyon approximated it from the files.">Estimated</span>
          </Chip>
        ) : undefined
      }
    >
      <Body load={load} retry={retry}>
        {(r) => {
          const servers = r.tool_servers ?? []
          const known = servers.length > 0 && servers.every((t) => t.tool_count != null)
          const tools = known ? servers.reduce((n, t) => n + (t.tool_count ?? 0), 0) : null
          const pol = r.policy?.permission_mode
          return (
            <>
              <RailRow label="Tools">
                {plural(servers.length, 'server')}
                {tools != null ? ` · ${tools} tools` : ''}
              </RailRow>
              <RailRow label="Skills">{r.skills?.length ?? 0}</RailRow>
              <RailRow label="Policy">{pol ?? NOT_REPORTED}</RailRow>
              <RailRow label="Scopes">{r.scopes?.length ? r.scopes.join(', ') : none}</RailRow>
              <RailRow label="Prompt">
                {r.prompt_parts?.length ? plural(r.prompt_parts.length, 'part') : NOT_REPORTED}
                {r.prompt_parts?.length && r.prompt_tokens_est != null ? ` · ${r.prompt_tokens_estimated ? '~' : ''}${r.prompt_tokens_est} tokens` : ''}
              </RailRow>
              {r.extends_chain && r.extends_chain.length > 1 && <RailRow label="Extends">{r.extends_chain.slice(1).join(' → ')}</RailRow>}
              {(r.notes ?? []).map((n) => (
                <Muted key={n}>{n}</Muted>
              ))}
            </>
          )
        }}
      </Body>
    </RailCard>
  )
}

/** Used by card; assigned items stay hidden until the backend reports them. */
export function UsedByCard({ load, retry }: { load: Load<UsedBy>; retry: () => void }) {
  return (
    <RailCard title="Used by">
      <Body load={load} retry={retry}>
        {(u) => (
          <>
            <RailRow label="Live sessions">
              {u.live_sessions?.length ?? 0}
              {(u.live_sessions?.length ?? 0) > 0 && (
                <>
                  {' · '}
                  <Link className="text-fg-secondary underline underline-offset-2 hover:text-fg" to="/agents">
                    Open on Agents ▸
                  </Link>
                </>
              )}
            </RailRow>
            <RailRow label="Launches">{u.launches?.length ?? 0}</RailRow>
            <RailRow label="Teams">{u.teams?.length ?? 0}</RailRow>
          </>
        )}
      </Body>
    </RailCard>
  )
}

const MARK = { ok: '✓', warn: '⚠', fail: '✕' } as const

/** Checks card: text first. Only a failed check is danger. */
export function ChecksCard({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.checks(id), [id])
  return (
    <RailCard title="Checks">
      <Body load={load} retry={retry}>
        {(c) =>
          c.checks.length ? (
            c.checks.map((x) => (
              <RailRow key={x.name + (x.entity ?? '')} tone={x.status === 'fail' ? 'danger' : undefined}>
                <>
                  {MARK[x.status]} {x.status === 'fail' ? 'Failed: ' : ''}
                  {x.message}
                </>
              </RailRow>
            ))
          ) : (
            <Muted>No checks reported.</Muted>
          )
        }
      </Body>
    </RailCard>
  )
}

/** The Used by section of the form: the same counts as chips. */
export function UsedBySection({ load, retry }: { load: Load<UsedBy>; retry: () => void }) {
  return (
    <Block id="used_by">
      <Body load={load} retry={retry}>
        {(u) => (
          <ul className="flex flex-wrap gap-1.5">
            <li>
              <Chip>{plural(u.live_sessions?.length ?? 0, 'live session')}</Chip>
            </li>
            <li>
              <Chip>{plural(u.launches?.length ?? 0, 'launch', 'launches')}</Chip>
            </li>
            <li>
              <Chip>{plural(u.teams?.length ?? 0, 'team')}</Chip>
            </li>
            {(u.extended_by?.length ?? 0) > 0 && (
              <li>
                <Chip>extended by {u.extended_by?.length}</Chip>
              </li>
            )}
          </ul>
        )}
      </Body>
    </Block>
  )
}

function FoldedDiff({ text }: { text: string }) {
  const long = text.split('\n').length > 8
  const [open, setOpen] = useState(!long)
  return (
    <div className="mt-1">
      {long && (
        <Button type="button" variant="outline" size="sm" onClick={() => setOpen((o) => !o)}>
          {open ? 'Hide changes' : 'Show changes'}
        </Button>
      )}
      {open && <Diff text={text} />}
    </div>
  )
}

export function HistorySection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.history(id), [id])
  const [more, setMore] = useState<HistoryEntry[]>([])
  const [cursor, setCursor] = useState<string | null | undefined>(undefined)
  const [busy, setBusy] = useState(false)
  const [moreError, setMoreError] = useState('')
  return (
    <Block id="history">
      <Body load={load} retry={retry}>
        {(h) => {
          const entries = [...h.entries, ...more]
          const next = cursor === undefined ? h.next_cursor : cursor
          if (!entries.length) return <span className="text-control text-fg-faint">No applied changes yet.</span>
          return (
            <div className="space-y-2">
              <ul className="space-y-2.5">
                {entries.map((e) => (
                  <li key={e.plan_id} className="text-xs">
                    <p className="flex flex-wrap items-baseline gap-x-2">
                      <b className="text-fg">{e.name || e.plan_id}</b>
                      <span className="text-fg-muted">
                        {e.actor || NOT_REPORTED} · <span title={absolute(e.at)}>{relativeTime(e.at)}</span>
                        {e.activation ? ` · ${e.activation}` : ''}
                      </span>
                    </p>
                    {e.diff ? <FoldedDiff text={e.diff} /> : <p className="text-fg-faint">no diff recorded</p>}
                  </li>
                ))}
              </ul>
              {next && (
                <Button
                  type="button"
                  disabled={busy}
                  variant="outline"
                  size="sm"
                  onClick={async () => {
                    setBusy(true)
                    setMoreError('')
                    try {
                      const page = await builderApi.history(id, next)
                      setMore((m) => [...m, ...page.entries])
                      setCursor(page.next_cursor ?? null)
                    } catch (e) {
                      setMoreError(errorMessage(e))
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  {busy ? 'Loading' : 'Load more'}
                </Button>
              )}
              {moreError && (
                <p role="alert" className="text-xs text-fg-secondary">
                  Could not load more. {moreError}
                </p>
              )}
            </div>
          )
        }}
      </Body>
    </Block>
  )
}
