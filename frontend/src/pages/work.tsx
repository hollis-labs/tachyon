import { Button, EmptyState, Input, Label, Skeleton } from "@hollis-labs/design-components"
import { PageHeader, SummaryCards } from "@hollis-labs/kit-dashboard"
import { RefreshCw } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import type { Agent, WorkItem } from "../api/client"
import { useApi } from "../api/context"
import { PendingApprovalError } from "../api/hitl"
import { type AskDetail, useVerbs } from "../api/verbs"
import { ACTIVE_STATUSES, CLOSED_STATUSES, workPageInfo } from "../api/work"
import { PendingApproval } from "../components/pending-approval"
import { workSearchNotice } from "../components/work/search-notice"
import { assigneeOf, dataOf, WorkDetail } from "../components/work/work-detail"

const selectStyle = "h-8 rounded-md border border-border bg-surface px-2 text-sm text-text"
const message = (error: unknown) => (error instanceof Error ? error.message : String(error))

export function WorkPage() {
  const verbs = useVerbs()
  const api = useApi()
  const canList = verbs.has("work_list")
  const canSearch = verbs.has("work_search")
  const [tasks, setTasks] = useState<WorkItem[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [agentError, setAgentError] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [pending, setPending] = useState<AskDetail | null>(null)
  const [agentPending, setAgentPending] = useState<AskDetail | null>(null)
  const [search, setSearch] = useState("")
  const [searchResult, setSearchResult] = useState<{ query: string; notice?: string } | null>(null)
  const [status, setStatus] = useState("")
  const [scopeInput, setScopeInput] = useState({ project: "", tags: "" })
  const [scope, setScope] = useState({ project: "", tags: "" })
  const [pageInfo, setPageInfo] = useState<ReturnType<typeof workPageInfo> | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState("")
  const generation = useRef(0)
  const [assignee, setAssignee] = useState("")
  const [revision, setRevision] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  // The refresh counter deliberately restarts this effect after mutations.
  // biome-ignore lint/correctness/useExhaustiveDependencies: revision triggers a new read after explicit refresh or mutation.
  useEffect(() => {
    if (!canList) {
      setLoading(false)
      return
    }
    let active = true
    generation.current++
    setTasks([])
    setPageInfo(null)
    setLoadingMore(false)
    setMoreError("")
    setLoading(true)
    const timer = setTimeout(
      async () => {
        setLoading(true)
        setError("")
        setPending(null)
        setSearchResult(null)
        try {
          const query = search.trim()
          let items: WorkItem[]
          if (query && canSearch) {
            const page = dataOf(await api.searchWork(query))
            items = page.tasks
            if (active) setSearchResult({ query, notice: workSearchNotice(page) })
          } else {
            const page = dataOf(
              await api.listWork({
                ...(status ? { status } : {}),
                ...(scope.project ? { project_id: scope.project } : {}),
                ...(scope.tags ? { tags: scope.tags } : {}),
                limit: 50,
                offset: 0,
              }),
            )
            const info = workPageInfo(page, 0, { status, project_id: scope.project })
            items = page.tasks
            if (active) setPageInfo(info)
          }
          if (active) setTasks(items)
        } catch (error) {
          if (active) {
            if (error instanceof PendingApprovalError) setPending(error.ask)
            else {
              setTasks([])
              setError(message(error))
            }
          }
        } finally {
          if (active) setLoading(false)
        }
      },
      search ? 250 : 0,
    )
    return () => {
      active = false
      generation.current++
      clearTimeout(timer)
    }
  }, [api, canList, canSearch, search, status, scope, revision])

  async function loadMore() {
    if (pageInfo?.next === undefined || loadingMore) return
    const request = generation.current
    const offset = pageInfo.next
    setLoadingMore(true)
    setMoreError("")
    try {
      const page = dataOf(
        await api.listWork({
          ...(status ? { status } : {}),
          ...(scope.project ? { project_id: scope.project } : {}),
          ...(scope.tags ? { tags: scope.tags } : {}),
          limit: 50,
          offset,
        }),
      )
      const info = workPageInfo(page, offset, { status, project_id: scope.project })
      if (request !== generation.current) return
      setTasks((current) =>
        Array.from(new Map([...current, ...page.tasks].map((task) => [task.id, task])).values()),
      )
      setPageInfo(info)
    } catch (error) {
      if (request === generation.current) {
        if (error instanceof PendingApprovalError) setPending(error.ask)
        else setMoreError(message(error))
      }
    } finally {
      if (request === generation.current) setLoadingMore(false)
    }
  }

  const canListAgents = verbs.has("agent_list")
  useEffect(() => {
    if (!canListAgents) {
      setAgents([])
      return
    }
    let active = true
    setAgentError("")
    setAgentPending(null)
    api
      .listWorkAgents()
      .then((result) => {
        if (active) setAgents(dataOf(result))
      })
      .catch((error) => {
        if (active) {
          setAgents([])
          if (error instanceof PendingApprovalError) setAgentPending(error.ask)
          else setAgentError(`Could not load agents: ${message(error)}`)
        }
      })
    return () => {
      active = false
    }
  }, [api, canListAgents])

  const statuses = useMemo(
    () =>
      Array.from(
        new Set([...ACTIVE_STATUSES, ...CLOSED_STATUSES, ...tasks.map((task) => task.status)]),
      ).sort(),
    [tasks],
  )
  const assignees = useMemo(
    () => Array.from(new Set(tasks.map(assigneeOf).filter(Boolean))).sort(),
    [tasks],
  )
  const agentName = (id: string) => agents.find((agent) => agent.id === id)?.name ?? id
  const visibleTasks = tasks.filter(
    (task) =>
      (!status || task.status === status) &&
      (!assignee ||
        (assignee === "unassigned" ? !assigneeOf(task) : assigneeOf(task) === assignee)),
  )

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Work Tracking">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={loading || !canList}
          onClick={() => setRevision((value) => value + 1)}
        >
          <RefreshCw className="mr-2 h-3.5 w-3.5" />
          Refresh
        </Button>
      </PageHeader>
      {agentPending && (
        <div className="px-4">
          <PendingApproval ask={agentPending} />
        </div>
      )}
      {verbs.loading ? (
        <Skeleton className="m-4 h-40" />
      ) : !canList ? (
        <EmptyState
          variant="empty"
          title="Work tracking unavailable"
          description="The connected work provider does not support task lists."
        />
      ) : (
        <>
          {!loading && !error && !pending && (
            <SummaryCards
              cards={[
                {
                  label: search.trim() && canSearch ? "Returned tasks" : "Shown loaded tasks",
                  value: visibleTasks.length,
                },
                {
                  label: "Doing (loaded)",
                  value: visibleTasks.filter((task) => task.status === "doing").length,
                },
                {
                  label: "Unassigned (loaded)",
                  value: visibleTasks.filter((task) => !assigneeOf(task)).length,
                },
              ]}
            />
          )}
          <div className="flex flex-wrap items-end gap-3 border-b border-border px-4 py-3">
            {canSearch && (
              <div className="min-w-48 flex-1">
                <Label htmlFor="work-search">Search</Label>
                <Input
                  id="work-search"
                  placeholder="Search tasks"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              </div>
            )}
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(event) => {
                event.preventDefault()
                setScope({ project: scopeInput.project.trim(), tags: scopeInput.tags.trim() })
              }}
            >
              <div>
                <Label htmlFor="work-project">Project ID</Label>
                <Input
                  id="work-project"
                  placeholder="Provider default"
                  value={scopeInput.project}
                  onChange={(event) =>
                    setScopeInput((current) => ({ ...current, project: event.target.value }))
                  }
                />
              </div>
              <div>
                <Label htmlFor="work-tags">Tags (comma-separated)</Label>
                <Input
                  id="work-tags"
                  value={scopeInput.tags}
                  onChange={(event) =>
                    setScopeInput((current) => ({ ...current, tags: event.target.value }))
                  }
                />
              </div>
              <Button type="submit" variant="outline" size="sm">
                Apply scope
              </Button>
            </form>
            <div className="flex flex-col gap-1">
              <Label htmlFor="work-status">Status</Label>
              <select
                id="work-status"
                className={selectStyle}
                value={status}
                onChange={(event) => setStatus(event.target.value)}
              >
                <option value="">All statuses</option>
                {Array.from(new Set([...statuses, ...(status ? [status] : [])])).map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="work-assignee">Assignee (loaded tasks)</Label>
              <select
                id="work-assignee"
                className={selectStyle}
                value={assignee}
                onChange={(event) => setAssignee(event.target.value)}
              >
                <option value="">All assignees</option>
                <option value="unassigned">Unassigned</option>
                {Array.from(
                  new Set([
                    ...assignees,
                    ...(assignee && assignee !== "unassigned" ? [assignee] : []),
                  ]),
                ).map((value) => (
                  <option key={value} value={value}>
                    {agentName(value)}
                  </option>
                ))}
              </select>
            </div>
            {(search ||
              status ||
              assignee ||
              scopeInput.project ||
              scopeInput.tags ||
              scope.project ||
              scope.tags) && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setSearch("")
                  setStatus("")
                  setAssignee("")
                  setScopeInput({ project: "", tags: "" })
                  setScope({ project: "", tags: "" })
                }}
              >
                Clear filters
              </Button>
            )}
          </div>
          <div className="flex-1 overflow-auto">
            {!loading && !error && !pending && (
              <p role="status" className="px-4 py-3 text-sm text-text-muted">
                {search.trim() && canSearch
                  ? "Provider search results. Status and assignee filter returned results; project and tags do not apply to search."
                  : assignee
                    ? `Filtering ${tasks.length} loaded${pageInfo?.total === undefined ? "; total unavailable" : ` of ${pageInfo.total} matching tasks`} by assignee.`
                    : `${tasks.length} loaded${pageInfo?.total === undefined ? "; total unavailable" : ` of ${pageInfo.total} matching tasks`}.`}
              </p>
            )}
            {!loading &&
              !error &&
              !pending &&
              searchResult?.query === search.trim() &&
              searchResult.notice && (
                <p role="status" className="px-4 py-3 text-sm text-text-muted break-words">
                  {searchResult.notice}
                </p>
              )}
            {pending ? (
              <div className="p-4">
                <PendingApproval ask={pending} />
              </div>
            ) : error ? (
              <p role="alert" className="p-4 text-sm text-status-failed">
                {error}
              </p>
            ) : loading ? (
              <div className="space-y-2 p-4">
                <Skeleton className="h-16 w-full" />
                <Skeleton className="h-16 w-full" />
                <Skeleton className="h-16 w-full" />
              </div>
            ) : visibleTasks.length === 0 ? (
              <EmptyState
                variant="no-results"
                title="No matching tasks"
                description="Try adjusting your filters or search query."
              />
            ) : (
              <div className="divide-y divide-border">
                {visibleTasks.map((task) => (
                  <button
                    key={task.id}
                    type="button"
                    disabled={!verbs.has("work_read")}
                    onClick={() => setSelectedId(task.id)}
                    className="flex w-full items-center gap-4 bg-surface px-4 py-3 text-left transition-colors enabled:hover:bg-panel-1 disabled:cursor-default"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-sm font-medium">{task.title}</span>
                        <span className="text-xs uppercase tracking-wider text-text-soft">
                          {task.status}
                        </span>
                      </div>
                      <div className="mt-1 flex flex-wrap gap-3 text-xs text-text-subtle">
                        <span>{task.id}</span>
                        <span>Priority {task.priority}</span>
                        <span>{assigneeOf(task) ? agentName(assigneeOf(task)) : "Unassigned"}</span>
                      </div>
                    </div>
                  </button>
                ))}
              </div>
            )}
            {!loading &&
              !error &&
              !pending &&
              !(search.trim() && canSearch) &&
              pageInfo?.hasMore && (
                <div className="space-y-2 px-4 py-3">
                  {moreError && (
                    <p role="alert" className="text-sm text-status-failed">
                      {moreError}
                    </p>
                  )}
                  <Button
                    type="button"
                    variant="outline"
                    disabled={loadingMore}
                    onClick={() => void loadMore()}
                  >
                    {loadingMore
                      ? "Loading more…"
                      : pageInfo.more === undefined
                        ? "Load more"
                        : `${pageInfo.more} more`}
                  </Button>
                </div>
              )}
          </div>
        </>
      )}
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
    </div>
  )
}
