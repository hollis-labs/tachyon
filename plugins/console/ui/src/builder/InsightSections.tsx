import { type ReactNode, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { type Check, type HistoryEntry, builderApi, errorMessage } from './api'
import { relativeTime } from './changes'
import { Diff } from './ReviewDialog'
import { type Load, useLoad } from './useLoad'
import { NOT_REPORTED, SECTIONS } from './vocab'

const CHECK_WORD = { ok: 'Passed', warn: 'Warning', fail: 'Failed' } as const

function Block({ id, children }: { id: string; children: ReactNode }) {
  const label = SECTIONS.find((s) => s.id === id)?.label ?? id
  return (
    <section id={`section-${id}`} aria-labelledby={`h-${id}`} className="scroll-mt-4 px-5 py-4">
      <h3 id={`h-${id}`} className="mb-2 dash-label font-medium text-fg-muted">
        {label}
      </h3>
      {children}
    </section>
  )
}

function Body<T>({ load, retry, children }: { load: Load<T>; retry: () => void; children: (d: T) => ReactNode }) {
  if (load.status === 'loading') return <div aria-busy="true" className="h-12 animate-pulse bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="flex items-center gap-3">
        <span className="dash-body text-fg-secondary">{load.message}</span>
        <Button type="button" onClick={retry} variant="outline" size="sm">
          Retry
        </Button>
      </div>
    )
  return <>{children(load.data)}</>
}

function Item({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1 border-b border-border-subtle py-2 md:grid-cols-[10rem_1fr] md:gap-3">
      <dt className="dash-support text-fg-muted">{label}</dt>
      <dd className="min-w-0 dash-body text-fg [overflow-wrap:anywhere]">{children}</dd>
    </div>
  )
}

const none = <span className="text-fg-faint">none</span>
const absolute = (at: number) => new Date(at > 1e12 ? at : at * 1000).toLocaleString()

export function ResolvedSection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.resolved(id), [id])
  return (
    <Block id="resolved">
      <Body load={load} retry={retry}>
        {(r) => (
          <dl>
            <p className="dash-support text-fg-muted">{r.resolved_by === 'tether' ? 'Computed by Tether.' : 'Approximated by Tachyon; Tether did not compute this.'}</p>
            <Item label="Tool servers">{r.tool_servers?.length ? r.tool_servers.map((t) => `${t.id} (${t.status}${t.tool_count != null ? `, ${t.tool_count} tools` : ''})`).join(', ') : none}</Item>
            <Item label="Skills">{r.skills?.length ? r.skills.map((x) => x.name || x.id).join(', ') : none}</Item>
            <Item label="Scopes">{r.scopes?.length ? r.scopes.join(', ') : none}</Item>
            <Item label="Extends">{r.extends_chain?.length ? r.extends_chain.join(' → ') : none}</Item>
            <Item label="Prompt">{r.prompt_tokens_est != null ? `${r.prompt_tokens_estimated ? '~' : ''}${r.prompt_tokens_est} tokens` : NOT_REPORTED}</Item>
            {(r.notes ?? []).map((n) => (
              <p key={n} className="dash-support text-fg-muted">
                {n}
              </p>
            ))}
          </dl>
        )}
      </Body>
    </Block>
  )
}

export function UsedBySection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.usedBy(id), [id])
  return (
    <Block id="used_by">
      <Body load={load} retry={retry}>
        {(u) => (
          <dl>
            <Item label="Live sessions">{u.live_sessions?.length ?? 0}</Item>
            <Item label="Launches">{u.launches?.length ? u.launches.map((l) => `${l.project}${l.enabled ? '' : ' (disabled)'}`).join(', ') : none}</Item>
            <Item label="Teams">{u.teams?.length ? u.teams.map((t) => t.name).join(', ') : none}</Item>
            <Item label="Extended by">{u.extended_by?.length ? u.extended_by.join(', ') : none}</Item>
            <Item label="Assigned items">{NOT_REPORTED}</Item>
          </dl>
        )}
      </Body>
    </Block>
  )
}

function CheckRow({ c }: { c: Check }) {
  return (
    <li className="dash-body text-fg-secondary">
      <span className={c.status === 'fail' ? 'text-danger' : ''}>{CHECK_WORD[c.status]}</span> {c.message}
      {c.remedy ? ` ${c.remedy}` : ''}
    </li>
  )
}

export function ChecksSection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.checks(id), [id])
  return (
    <Block id="checks">
      <Body load={load} retry={retry}>
        {(c) =>
          c.checks.length ? (
            <ul className="space-y-1">
              {c.checks.map((x) => (
                <CheckRow key={x.name + (x.entity ?? '')} c={x} />
              ))}
            </ul>
          ) : (
            <span className="dash-body text-fg-faint">No checks reported.</span>
          )
        }
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
          if (!entries.length) return <span className="dash-body text-fg-faint">No applied changes yet.</span>
          return (
            <div className="space-y-2">
              <ul className="space-y-2">
                {entries.map((e) => (
                  <li key={e.plan_id} className="border border-border-subtle p-2 dash-body text-fg">
                    <p>
                      {e.name || e.plan_id}{' '}
                      <span className="dash-support text-fg-muted">
                        <span title={absolute(e.at)}>{relativeTime(e.at)}</span> · {e.actor || NOT_REPORTED}
                      </span>
                    </p>
                    {e.activation && <p className="dash-support text-fg-secondary">{e.activation}</p>}
                    {e.diff ? <FoldedDiff text={e.diff} /> : <p className="dash-support text-fg-faint">No diff held for this change.</p>}
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
                <p role="alert" className="dash-support text-fg-secondary">
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
