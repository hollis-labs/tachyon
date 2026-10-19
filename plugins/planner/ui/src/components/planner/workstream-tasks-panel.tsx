import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { PlannerRowContent, PlannerTableHeader } from '@planner/components/planner/planner-table'
import { usePlannerApi as useApi } from '@planner/api/context'
import type { PlannerWorkstreamTasks } from '@planner/api/types'

const ORDER = ['doing', 'review', 'queued', 'blocked', 'paused', 'todo', 'backlog', 'done', 'archived', 'abandoned', 'cancelled']

/** The Torque tasks behind a workstream: status counts and the open ones. */
export function WorkstreamTasksPanel({ id, refreshKey }: { id: string; refreshKey?: unknown }) {
  const api = useApi()
  const [data, setData] = useState<PlannerWorkstreamTasks | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const ctl = new AbortController()
    api.plannerWorkstreamTasks(id, ctl.signal).then((d) => { setData(d); setFailed(false) }).catch(() => { if (!ctl.signal.aborted) setFailed(true) })
    return () => ctl.abort()
  }, [api, id, refreshKey])

  if (failed) return <p role="status" className="dash-body text-warning">Execution tasks are currently unavailable. Planner details remain available.</p>
  if (!data) return null
  const scope = [...data.torque_project_ids, data.torque_tag ? `tag ${data.torque_tag}` : ''].filter(Boolean).join(' · ')
  const counts = ORDER.filter((s) => data.counts[s]).map((s) => ({ status: s, n: data.counts[s] }))

  return (
    <section aria-label="Torque tasks" className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="dash-body leading-5 font-semibold">Torque tasks{data.total ? ` (${data.total})` : ''}</h3>
        {scope && <span className="truncate dash-label leading-4 text-fg-muted">{scope}</span>}
      </div>
      {data.note ? (
        <p className="dash-body leading-5 text-fg-muted">{data.note} Set torque_project_ids or torque_tag on it to see its tasks here.</p>
      ) : data.total === 0 ? (
        <p className="dash-body leading-5 text-fg-muted">No Torque tasks match this workstream&apos;s projects and tag.</p>
      ) : (
        <>
          <div className="flex flex-wrap gap-1.5">
            {counts.map((c) => (
              <span key={c.status} className="inline-flex items-center gap-1.5">
                <PlannerStatus status={c.status} />
                <span className="dash-label leading-4 text-fg-muted">{c.n}</span>
              </span>
            ))}
          </div>
          {data.open.length > 0 && (
            <div className="min-w-0 border-t border-border">
            <PlannerTableHeader trailing="" />
            <ul>
              {data.open.map((t) => (
                <li key={t.id} className="dash-record-list-row">
                  <Link to={`/tasks/${encodeURIComponent(t.id)}`} className="planner-row-trigger dash-record-grid" aria-label={`Open ${t.id}: ${t.title}`}>
                    <PlannerRowContent title={t.title} metadata={<span>{t.id}</span>} status={<PlannerStatus status={t.status} />} />
                  </Link>
                </li>
              ))}
            </ul>
            </div>
          )}
          {data.open_total > data.open.length && <p className="dash-label leading-4 text-fg-muted">Showing {data.open.length} of {data.open_total} open tasks.</p>}
        </>
      )}
    </section>
  )
}
