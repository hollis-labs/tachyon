import { Skeleton } from "@hollis-labs/sysop-ui"
import { useEffect, useState } from "react"
import type { DurableAgent, DurableAgentEvent, DurableAgentSession } from "../../api/client"
import { useApi } from "../../api/context"
import { LargeDialog } from "./large-dialog"

interface DurableAgentInspectDialogProps {
  agent: DurableAgent | null
  onClose: () => void
}

function Field({ label, value }: { label: string; value?: string | null }) {
  if (!value) return null
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-wider text-text-subtle">{label}</span>
      <span className="text-sm text-text break-all">{value}</span>
    </div>
  )
}

function SectionHeading({ children }: { children: string }) {
  return (
    <h3 className="text-xs font-medium uppercase tracking-wider text-text-subtle mb-2">
      {children}
    </h3>
  )
}

export function DurableAgentInspectDialog({ agent, onClose }: DurableAgentInspectDialogProps) {
  const api = useApi()
  const [events, setEvents] = useState<DurableAgentEvent[]>([])
  const [sessions, setSessions] = useState<DurableAgentSession[]>([])
  const [loading, setLoading] = useState(false)

  // Refresh activity/sessions each time a different (or the same, reopened)
  // durable agent is inspected — this view is read-only, so a fresh load on
  // open is enough; there's no local mutation to reconcile against.
  useEffect(() => {
    if (!agent) {
      setEvents([])
      setSessions([])
      return
    }
    let cancelled = false
    setLoading(true)
    Promise.all([api.listDurableAgentEvents(agent.id), api.listDurableAgentSessions(agent.id)])
      .then(([e, s]) => {
        if (cancelled) return
        setEvents(e)
        setSessions(s)
      })
      .catch((error) => console.error("Failed to load durable agent activity:", error))
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [agent, api])

  return (
    <LargeDialog
      open={!!agent}
      onClose={onClose}
      title={agent?.name ?? ""}
      description={[agent?.status, agent?.provider, agent?.model].filter(Boolean).join(" · ")}
    >
      {agent && (
        <div className="flex flex-col gap-6">
          <div>
            <SectionHeading>Overview</SectionHeading>
            <div className="grid grid-cols-2 gap-3 rounded border border-border bg-panel-1 p-3 sm:grid-cols-3">
              <Field label="ID" value={agent.id} />
              <Field label="Slug" value={agent.slug} />
              <Field label="Profile ID" value={agent.profile_id} />
              <Field label="Lifecycle Class" value={agent.lifecycle_class} />
              <Field label="Runtime Kind" value={agent.runtime_kind} />
              <Field label="Current Session" value={agent.current_session_id} />
              <Field
                label="Created"
                value={agent.created_at ? new Date(agent.created_at).toLocaleString() : undefined}
              />
              <Field
                label="Updated"
                value={agent.updated_at ? new Date(agent.updated_at).toLocaleString() : undefined}
              />
            </div>
            {agent.failure_reason && (
              <div className="mt-2 rounded border border-status-failed/40 bg-status-failed/10 px-3 py-2 text-xs text-status-failed">
                {agent.failure_reason}
              </div>
            )}
          </div>

          <div>
            <SectionHeading>Recent Activity</SectionHeading>
            {loading ? (
              <div className="flex flex-col gap-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <Skeleton key={i} className="h-10 w-full rounded-md" />
                ))}
              </div>
            ) : events.length === 0 ? (
              <p className="text-xs text-text-subtle">No recorded activity yet.</p>
            ) : (
              <div className="flex flex-col divide-y divide-border rounded border border-border">
                {events.map((event) => (
                  <div key={event.id} className="flex flex-col gap-0.5 px-3 py-2">
                    <div className="flex items-center gap-2 text-xs">
                      <span className="font-medium text-text">{event.event_type}</span>
                      {event.status_before && event.status_after && (
                        <span className="text-text-subtle">
                          {event.status_before} → {event.status_after}
                        </span>
                      )}
                      <span className="ml-auto text-text-subtle">
                        {new Date(event.created_at).toLocaleString()}
                      </span>
                    </div>
                    {event.message && <p className="text-xs text-text-subtle">{event.message}</p>}
                  </div>
                ))}
              </div>
            )}
          </div>

          <div>
            <SectionHeading>Attached Sessions</SectionHeading>
            {loading ? (
              <Skeleton className="h-10 w-full rounded-md" />
            ) : sessions.length === 0 ? (
              <p className="text-xs text-text-subtle">No sessions attached.</p>
            ) : (
              <div className="flex flex-col divide-y divide-border rounded border border-border">
                {sessions.map((session) => (
                  <div
                    key={session.session_id}
                    className="flex items-center gap-3 px-3 py-2 text-xs"
                  >
                    <span className="font-medium text-text">{session.session_id}</span>
                    {session.relation && (
                      <span className="text-text-subtle">{session.relation}</span>
                    )}
                    {session.session_status && (
                      <span className="text-text-subtle">{session.session_status}</span>
                    )}
                    {session.runtime_state && (
                      <span className="text-text-subtle">{session.runtime_state}</span>
                    )}
                    {session.attached_at && (
                      <span className="ml-auto text-text-subtle">
                        {new Date(session.attached_at).toLocaleString()}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </LargeDialog>
  )
}
