import { type ReactNode, useState } from 'react'
import { type Check, type HistoryEntry, builderApi } from './api'
import { useLoad, type Load } from './useLoad'
import { NOT_REPORTED, SECTIONS } from './vocab'

const BTN = 'border border-border px-2 py-1 dash-control text-fg-secondary hover:bg-surface-hover'
const CHECK_WORD = { ok: 'Passed', warn: 'Warning', fail: 'Failed' } as const

function Block({ id, children }: { id: string; children: ReactNode }) {
  const label = SECTIONS.find((s) => s.id === id)?.label ?? id
  return (
    <section id={`section-${id}`} aria-labelledby={`h-${id}`} className="scroll-mt-4 px-5 py-4">
      <h3 id={`h-${id}`} className="mb-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
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
        <button type="button" onClick={retry} className={BTN}>
          Retry
        </button>
      </div>
    )
  return <>{children(load.data)}</>
}

const none = <span className="text-fg-faint">none</span>
const when = (at: number) => new Date(at > 1e12 ? at : at * 1000).toLocaleString()

export function ResolvedSection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.resolved(id), [id])
  return (
    <Block id="resolved">
      <Body load={load} retry={retry}>
        {(r) => (
          <dl className="space-y-1 dash-body text-fg">
            <p className="dash-support text-fg-muted">{r.resolved_by === 'tether' ? 'Computed by Tether.' : 'Approximated by Tachyon; Tether did not compute this.'}</p>
            <div>Tool servers: {r.tool_servers?.length ? r.tool_servers.map((t) => `${t.id} (${t.status}${t.tool_count != null ? `, ${t.tool_count} tools` : ''})`).join(', ') : none}</div>
            <div>Skills: {r.skills?.length ? r.skills.map((x) => x.name || x.id).join(', ') : none}</div>
            <div>Scopes: {r.scopes?.length ? r.scopes.join(', ') : none}</div>
            <div>Extends: {r.extends_chain?.length ? r.extends_chain.join(' → ') : none}</div>
            <div>Prompt: {r.prompt_tokens_est != null ? `${r.prompt_tokens_estimated ? '~' : ''}${r.prompt_tokens_est} tokens` : NOT_REPORTED}</div>
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
          <dl className="space-y-1 dash-body text-fg">
            <div>Live sessions: {u.live_sessions?.length ?? 0}</div>
            <div>Launches: {u.launches?.length ? u.launches.map((l) => `${l.project}${l.enabled ? '' : ' (disabled)'}`).join(', ') : none}</div>
            <div>Teams: {u.teams?.length ? u.teams.map((t) => t.name).join(', ') : none}</div>
            <div>Extended by: {u.extended_by?.length ? u.extended_by.join(', ') : none}</div>
            <div>Assigned items: {NOT_REPORTED}</div>
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

export function HistorySection({ id }: { id: string }) {
  const [load, retry] = useLoad(() => builderApi.history(id), [id])
  const [more, setMore] = useState<HistoryEntry[]>([])
  const [cursor, setCursor] = useState<string | null | undefined>(undefined)
  const [busy, setBusy] = useState(false)
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
                        {when(e.at)} · {e.actor || NOT_REPORTED}
                      </span>
                    </p>
                    {e.activation && <p className="dash-support text-fg-secondary">{e.activation}</p>}
                    {e.diff ? (
                      <pre className="mt-1 max-h-40 overflow-auto bg-surface p-2 font-mono dash-support text-fg">{e.diff}</pre>
                    ) : (
                      <p className="dash-support text-fg-faint">No diff held for this change.</p>
                    )}
                  </li>
                ))}
              </ul>
              {next && (
                <button
                  type="button"
                  disabled={busy}
                  className={BTN}
                  onClick={async () => {
                    setBusy(true)
                    try {
                      const page = await builderApi.history(id, next)
                      setMore((m) => [...m, ...page.entries])
                      setCursor(page.next_cursor ?? null)
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  {busy ? 'Loading' : 'Load more'}
                </button>
              )}
            </div>
          )
        }}
      </Body>
    </Block>
  )
}
