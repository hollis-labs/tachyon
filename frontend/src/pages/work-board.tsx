import { Button, EmptyState, Input, Label, Skeleton } from "@hollis-labs/design-components"
import { ListPageLayout } from "@hollis-labs/kit-dashboard/layout"
import { PageHeader } from "@hollis-labs/kit-dashboard/ui"
import { useCallback, useEffect, useRef, useState } from "react"
import type { Agent, WorkItem } from "../api/client"
import { useApi } from "../api/context"
import { PendingApprovalError } from "../api/hitl"
import { type AskDetail, useVerbs } from "../api/verbs"
import { ACTIVE_STATUSES, CLOSED_STATUSES, workBoardApi } from "../api/work-board"
import { PendingApproval } from "../components/pending-approval"
import { workSearchNotice } from "../components/work/search-notice"
import { assigneeOf, dataOf, WorkDetail } from "../components/work/work-detail"

const message = (error: unknown) => (error instanceof Error ? error.message : String(error))
interface ColumnState {
  tasks: WorkItem[]
  lowerBound: number
  total?: number
  hasMore: boolean
  next?: number
  loading: boolean
  error: string
  pending?: AskDetail
}
const initialColumn: ColumnState = {
  tasks: [],
  lowerBound: 0,
  hasMore: false,
  loading: true,
  error: "",
}

function WorkCard({
  task,
  agents,
  canRead,
  onOpen,
}: {
  task: WorkItem
  agents: Agent[]
  canRead: boolean
  onOpen: (id: string) => void
}) {
  const assignee = assigneeOf(task)
  return (
    <button
      type="button"
      disabled={!canRead}
      onClick={() => onOpen(task.id)}
      className="w-full space-y-2 rounded border border-border bg-surface p-3 text-left enabled:hover:bg-panel-1 disabled:cursor-default"
    >
      <span className="block break-words font-medium">{task.title}</span>
      <span className="block break-all text-xs text-text-muted">{task.id}</span>
      <span className="block text-xs">
        {task.status} · Priority {task.priority}
      </span>
      <span className="block break-words text-xs">
        {assignee
          ? (agents.find((agent) => agent.id === assignee)?.name ?? assignee)
          : "Unassigned"}
      </span>
    </button>
  )
}

function WorkColumn({
  status,
  project,
  expanded,
  agents,
  canRead,
  onOpen,
}: {
  status: string
  project: string
  expanded: boolean
  agents: Agent[]
  canRead: boolean
  onOpen: (id: string) => void
}) {
  const [state, setState] = useState<ColumnState>(initialColumn)
  const generation = useRef(0)
  const load = useCallback(
    async (offset: number, append = false) => {
      const request = generation.current
      setState((current) => ({ ...current, loading: true, error: "", pending: undefined }))
      try {
        const page = dataOf(
          await workBoardApi.list({
            status,
            ...(project ? { project_id: project } : {}),
            limit: expanded ? 50 : 1,
            offset,
          }),
        )
        if (request !== generation.current) return
        if (!Array.isArray(page.tasks)) throw new Error("Task list response is incomplete.")
        if (page.tasks.some((task) => task.status !== status))
          throw new Error("Provider did not apply the requested status filter.")
        if (
          page.has_more &&
          (page.next_offset == null ||
            !Number.isInteger(page.next_offset) ||
            page.next_offset <= offset)
        )
          throw new Error("Task page continuation is invalid. Refresh this column.")
        // Provider totals are accepted only when structurally credible. Torque's
        // status/project-filtered totals are verified; offset pages are not a snapshot.
        setState((current) => {
          const tasks = expanded
            ? Array.from(
                new Map(
                  [...(append ? current.tasks : []), ...page.tasks].map((task) => [task.id, task]),
                ).values(),
              )
            : []
          const total =
            Number.isInteger(page.total) && page.total >= Math.max(tasks.length, page.tasks.length)
              ? page.total
              : undefined
          return {
            tasks,
            lowerBound: expanded ? tasks.length : page.tasks.length,
            total,
            hasMore: page.has_more,
            next: page.next_offset ?? undefined,
            loading: false,
            error: "",
          }
        })
      } catch (error) {
        if (request === generation.current)
          setState((current) => ({
            ...current,
            loading: false,
            error: error instanceof PendingApprovalError ? "" : message(error),
            pending: error instanceof PendingApprovalError ? error.ask : undefined,
          }))
      }
    },
    [status, project, expanded],
  )
  useEffect(() => {
    generation.current++
    setState(initialColumn)
    void load(0)
    return () => {
      generation.current++
    }
  }, [load])
  const count =
    state.total === undefined
      ? state.loading || state.error || state.pending
        ? "Count unavailable"
        : `at least ${state.lowerBound}`
      : `${state.total} total`
  return (
    <section
      aria-label={`${status} tasks`}
      className="min-w-0 space-y-3 rounded border border-border p-3 md:w-64 md:shrink-0 md:max-h-128 md:overflow-y-auto"
    >
      <h2 className="font-medium">{status}</h2>
      <p className="text-xs text-text-muted">{count}</p>
      {state.pending && <PendingApproval ask={state.pending} />}
      {state.error && (
        <p role="alert" className="break-words text-sm text-status-failed">
          {state.error}
        </p>
      )}
      {state.loading && (
        <div role="status" aria-label={`Loading ${status}`}>
          <Skeleton className="h-16" />
        </div>
      )}
      {expanded && (
        <>
          {state.tasks.map((task) => (
            <WorkCard key={task.id} task={task} agents={agents} canRead={canRead} onOpen={onOpen} />
          ))}
          {!state.loading && !state.error && !state.pending && state.tasks.length === 0 && (
            <p className="text-sm text-text-muted">No tasks</p>
          )}
          {state.tasks.length > 0 && (
            <p className="text-xs text-text-muted">{state.tasks.length} loaded</p>
          )}
          {!state.error && !state.pending && state.hasMore && (
            <Button
              variant="outline"
              disabled={state.loading}
              onClick={() => {
                if (state.next !== undefined) void load(state.next, true)
              }}
            >
              Load more
            </Button>
          )}
        </>
      )}
      {state.error && (
        <Button
          variant="outline"
          disabled={state.loading}
          onClick={() =>
            void load(
              state.tasks.length && state.next !== undefined ? state.next : 0,
              state.tasks.length > 0,
            )
          }
        >
          Retry
        </Button>
      )}
    </section>
  )
}

function BoardSearch({
  query,
  agents,
  canRead,
  onOpen,
}: {
  query: string
  agents: Agent[]
  canRead: boolean
  onOpen: (id: string) => void
}) {
  const api = useApi()
  const [state, setState] = useState<{
    tasks: WorkItem[]
    loading: boolean
    error: string
    pending?: AskDetail
    notice?: string
  }>({
    tasks: [],
    loading: true,
    error: "",
  })
  useEffect(() => {
    let active = true
    setState({ tasks: [], loading: true, error: "" })
    const timer = setTimeout(() => {
      api
        .searchWork(query)
        .then(dataOf)
        .then((page) => {
          if (active)
            setState({
              tasks: page.tasks,
              loading: false,
              error: "",
              notice: workSearchNotice(page),
            })
        })
        .catch((error) => {
          if (active)
            setState({
              tasks: [],
              loading: false,
              error: error instanceof PendingApprovalError ? "" : message(error),
              pending: error instanceof PendingApprovalError ? error.ask : undefined,
            })
        })
    }, 250)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [api, query])
  return (
    <section aria-label="Search results" className="space-y-3">
      <p className="text-sm text-text-muted">
        Provider search results. Project filter does not apply to search.
      </p>
      {state.pending ? (
        <PendingApproval ask={state.pending} />
      ) : state.loading ? (
        <Skeleton className="h-24" />
      ) : state.error ? (
        <p role="alert">{state.error}</p>
      ) : state.tasks.length ? (
        <>
          {state.notice ? (
            <p role="status" className="break-words text-sm text-text-muted">
              {state.notice}
            </p>
          ) : (
            <p>{state.tasks.length} returned search results</p>
          )}
          {state.tasks.map((task) => (
            <WorkCard key={task.id} task={task} agents={agents} canRead={canRead} onOpen={onOpen} />
          ))}
        </>
      ) : (
        <EmptyState
          variant="no-results"
          title="No matching tasks"
          description="Try another search."
        />
      )}
    </section>
  )
}

export function WorkBoardPage() {
  const verbs = useVerbs()
  const api = useApi()
  const [agents, setAgents] = useState<Agent[]>([])
  const [agentError, setAgentError] = useState("")
  const [agentPending, setAgentPending] = useState<AskDetail | null>(null)
  const [projectInput, setProjectInput] = useState("")
  const [project, setProject] = useState("")
  const [search, setSearch] = useState("")
  const [closed, setClosed] = useState(false)
  const [revision, setRevision] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const canList = verbs.has("work_list"),
    canRead = verbs.has("work_read"),
    canAgents = verbs.has("agent_list")
  useEffect(() => {
    let active = true
    setAgents([])
    setAgentError("")
    setAgentPending(null)
    if (canAgents)
      api
        .listWorkAgents()
        .then(dataOf)
        .then((items) => {
          if (active) setAgents(items)
        })
        .catch((error) => {
          if (active) {
            if (error instanceof PendingApprovalError) setAgentPending(error.ask)
            else setAgentError(message(error))
          }
        })
    return () => {
      active = false
    }
  }, [api, canAgents])
  return (
    <ListPageLayout
      header={
        <PageHeader title="Work Board">
          <Button
            variant="outline"
            disabled={!canList}
            onClick={() => setRevision((value) => value + 1)}
          >
            Refresh
          </Button>
        </PageHeader>
      }
    >
      <div className="space-y-4 p-4">
        {agentPending && <PendingApproval ask={agentPending} />}
        {verbs.loading ? (
          <Skeleton className="h-40" />
        ) : !canList ? (
          <EmptyState
            variant="empty"
            title="Work Board unavailable"
            description={
              verbs.available === true
                ? "The connected provider does not offer task lists."
                : "Could not discover work capabilities."
            }
          />
        ) : (
          <>
            <div className="flex flex-wrap items-end gap-3">
              <form
                className="flex flex-wrap items-end gap-2"
                onSubmit={(event) => {
                  event.preventDefault()
                  setProject(projectInput)
                }}
              >
                <div>
                  <Label htmlFor="board-project">Project ID</Label>
                  <Input
                    id="board-project"
                    value={projectInput}
                    onChange={(event) => setProjectInput(event.target.value)}
                    placeholder="Provider default"
                  />
                </div>
                <Button variant="outline" type="submit">
                  Apply project
                </Button>
              </form>
              {verbs.has("work_search") && (
                <div>
                  <Label htmlFor="board-search">Search tasks</Label>
                  <Input
                    id="board-search"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                  />
                </div>
              )}
              {(project || search || projectInput) && (
                <Button
                  variant="outline"
                  onClick={() => {
                    setProject("")
                    setProjectInput("")
                    setSearch("")
                  }}
                >
                  Clear filters
                </Button>
              )}
              {!search.trim() && (
                <Button
                  variant="outline"
                  aria-expanded={closed}
                  onClick={() => setClosed((value) => !value)}
                >
                  {closed ? "Hide closed" : "Show closed"}
                </Button>
              )}
            </div>
            <p className="text-sm text-text-muted">
              Torque status columns. Custom provider statuses appear only in search.{" "}
              {project
                ? `Project: ${project}`
                : "Using the provider's configured default project scope."}
            </p>
            {agentError && (
              <p role="status" className="text-sm text-text-muted">
                Agent names unavailable: {agentError}
              </p>
            )}
            {search.trim() && verbs.has("work_search") ? (
              <BoardSearch
                key={`${search}:${revision}`}
                query={search.trim()}
                agents={agents}
                canRead={canRead}
                onOpen={setSelectedId}
              />
            ) : (
              <div className="flex flex-col gap-4 md:flex-row md:items-start md:overflow-x-auto">
                {[...ACTIVE_STATUSES, ...CLOSED_STATUSES].map((status) => (
                  <WorkColumn
                    key={`${status}:${project}:${revision}`}
                    status={status}
                    project={project}
                    expanded={!CLOSED_STATUSES.includes(status) || closed}
                    agents={agents}
                    canRead={canRead}
                    onOpen={setSelectedId}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>
      {selectedId && (
        <WorkDetail
          key={selectedId}
          id={selectedId}
          verbs={verbs}
          agents={agents}
          agentError={agentError}
          onClose={() => setSelectedId(null)}
          onUpdated={() => setRevision((value) => value + 1)}
        />
      )}
    </ListPageLayout>
  )
}
