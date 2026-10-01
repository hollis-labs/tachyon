import { Button, EmptyState, Input, Label, Skeleton } from "@hollis-labs/design-components"
import { ListPageLayout } from "@hollis-labs/kit-dashboard/layout"
import { PageHeader } from "@hollis-labs/kit-dashboard/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { boundedPatch, type RepoStatus, scmApi, scmData } from "../api/scm"
import { useVerbs } from "../api/verbs"

const selectClass = "h-9 max-w-full rounded-md border border-border bg-surface px-2 text-sm"
const message = (error: unknown) => (error instanceof Error ? error.message : String(error))

// Only reads, with stale replies ignored on selection, mode or capability changes.
function useRead<T>(enabled: boolean, load: () => Promise<T>) {
  const [state, setState] = useState<{ data?: T; error: string; loading: boolean }>({
    error: "",
    loading: enabled,
  })
  const [revision, setRevision] = useState(0)
  const requestRevision = useRef(0)
  useEffect(() => {
    requestRevision.current = revision
    let active = true
    setState({ error: "", loading: enabled })
    if (enabled)
      load()
        .then((data) => {
          if (active && requestRevision.current === revision)
            setState({ data, error: "", loading: false })
        })
        .catch((error) => {
          if (active && requestRevision.current === revision)
            setState({ error: message(error), loading: false })
        })
    return () => {
      active = false
    }
  }, [enabled, load, revision])
  return { ...state, retry: () => setRevision((value) => value + 1) }
}

function ReadRegion({
  loading,
  error,
  retry,
  children,
}: {
  loading: boolean
  error: string
  retry: () => void
  children: ReactNode
}) {
  if (loading)
    return (
      <div role="status" aria-label="Loading repository data">
        <Skeleton className="h-20 w-full" />
      </div>
    )
  if (error)
    return (
      <div>
        <p role="alert" className="break-words text-sm text-status-failed">
          {error}
        </p>
        <Button variant="outline" onClick={retry}>
          Retry
        </Button>
      </div>
    )
  return <>{children}</>
}

function StatusView({ status }: { status: RepoStatus }) {
  return (
    <div className="space-y-2 text-sm">
      <dl className="grid grid-cols-1 gap-2 break-words sm:grid-cols-2">
        <dt>Branch</dt>
        <dd>{status.branch || "No branch"}</dd>
        <dt>HEAD</dt>
        <dd className="break-all">{status.head || "No commits yet"}</dd>
        <dt>Upstream</dt>
        <dd>{status.upstream || "No upstream"}</dd>
        <dt>Ahead / behind (local refs)</dt>
        <dd>{status.upstream ? `${status.ahead} / ${status.behind}` : "No upstream comparison"}</dd>
        <dt>Working tree</dt>
        <dd>{status.dirty ? "Dirty" : "Clean"}</dd>
        <dt>CI</dt>
        <dd>Unknown - local Git only</dd>
      </dl>
      <h3 className="font-medium">Changes</h3>
      {(status.changes ?? []).length ? (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all">
          {status.changes.map((change) => change.replaceAll("\0", " → ")).join("\n")}
        </pre>
      ) : (
        <p>No changes</p>
      )}
    </div>
  )
}

function RepositoryDetail({ id }: { id: string }) {
  const verbs = useVerbs()
  const read = useCallback(() => scmApi.read(id).then(scmData), [id])
  const loadStatus = useCallback(() => scmApi.status(id).then(scmData), [id])
  const detail = useRead(verbs.has("scm_read"), read)
  const status = useRead(verbs.has("scm_status"), loadStatus)
  return (
    <section aria-label="Repository details" className="space-y-4 border-t border-border p-4">
      <h2 className="break-all text-lg font-medium">Repository: {id}</h2>
      {verbs.has("scm_read") ? (
        <ReadRegion {...detail}>
          <p className="break-all text-sm">{detail.data?.path}</p>
          <p className="text-sm">Local branches: {detail.data?.branches?.join(", ") || "None"}</p>
          {!verbs.has("scm_status") && detail.data && <StatusView status={detail.data.status} />}
        </ReadRegion>
      ) : (
        <p>Repository details unavailable</p>
      )}
      {verbs.has("scm_status") ? (
        <ReadRegion {...status}>
          <Button variant="outline" disabled={status.loading} onClick={status.retry}>
            Refresh status
          </Button>
          {status.data && <StatusView status={status.data} />}
        </ReadRegion>
      ) : (
        <p>Independent status refresh unavailable</p>
      )}
      <DiffView id={id} />
    </section>
  )
}

function DiffView({ id }: { id: string }) {
  const verbs = useVerbs()
  const [mode, setMode] = useState("unstaged")
  const [base, setBase] = useState("")
  const [head, setHead] = useState("")
  const [comparison, setComparison] = useState({ base: "", head: "" })
  const load = useCallback(
    () =>
      scmApi
        .diff({
          id,
          ...(mode === "staged" ? { staged: true } : mode === "commits" ? comparison : {}),
        })
        .then(scmData),
    [id, mode, comparison],
  )
  const result = useRead(verbs.has("scm_diff") && (mode !== "commits" || !!comparison.base), load)
  if (!verbs.has("scm_diff")) return <p>Diff unavailable</p>
  const patch = boundedPatch(result.data?.patch ?? "")
  return (
    <section className="space-y-3" aria-label="Read-only diff">
      <h3 className="font-medium">Diff</h3>
      <Label htmlFor="scm-diff-mode">Comparison</Label>
      <select
        id="scm-diff-mode"
        className={selectClass}
        value={mode}
        onChange={(event) => setMode(event.target.value)}
      >
        <option value="unstaged">Unstaged</option>
        <option value="staged">Staged</option>
        <option value="commits">Commits</option>
      </select>
      {mode === "commits" && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            setComparison({ base, head })
          }}
        >
          <div>
            <Label htmlFor="scm-base">Base revision</Label>
            <Input id="scm-base" value={base} onChange={(event) => setBase(event.target.value)} />
          </div>
          <div>
            <Label htmlFor="scm-head">Head revision (default HEAD)</Label>
            <Input id="scm-head" value={head} onChange={(event) => setHead(event.target.value)} />
          </div>
          <Button variant="outline" type="submit" disabled={!base}>
            Compare
          </Button>
        </form>
      )}
      <ReadRegion {...result}>
        {result.data && (
          <>
            {patch.truncated && (
              <p role="status">
                Diff truncated: showing {patch.shownLines} of {patch.totalLines} lines;{" "}
                {patch.shownBytes} of {patch.totalBytes} bytes.
              </p>
            )}
            {patch.text ? (
              <pre className="max-h-96 overflow-auto whitespace-pre text-xs">{patch.text}</pre>
            ) : (
              <p>No diff for this comparison</p>
            )}
          </>
        )}
      </ReadRegion>
    </section>
  )
}

function SCMPage({ activity = false }: { activity?: boolean }) {
  const verbs = useVerbs()
  const canList = verbs.has("scm_list")
  const canActivity = verbs.has("scm_activity")
  const list = useRead(
    canList,
    useCallback(() => scmApi.list().then(scmData), []),
  )
  const [selected, setSelected] = useState("")
  const [input, setInput] = useState("")
  const [limit, setLimit] = useState(20)
  const id = selected || list.data?.[0]?.id || ""
  const history = useRead(
    activity && canActivity && !!id,
    useCallback(() => scmApi.activity(id, limit).then(scmData), [id, limit]),
  )
  const canPage = activity ? canActivity : canList
  return (
    <ListPageLayout
      header={
        <PageHeader title={activity ? "Source Activity" : "Repositories"}>
          <Button
            variant="outline"
            disabled={!canPage}
            onClick={() => {
              list.retry()
              history.retry()
            }}
          >
            Refresh
          </Button>
        </PageHeader>
      }
    >
      <div className="space-y-4 p-4">
        {verbs.loading ? (
          <Skeleton className="h-32" />
        ) : !canPage ? (
          <EmptyState
            variant="empty"
            title="Source control unavailable"
            description={
              verbs.available === true
                ? "The required read verb is unavailable."
                : "Could not discover source control capabilities."
            }
          />
        ) : (
          <>
            {canList ? (
              <ReadRegion {...list}>
                {list.data?.length ? (
                  <>
                    <Label htmlFor="scm-repository">Repository</Label>
                    <select
                      id="scm-repository"
                      className={`${selectClass} w-full`}
                      value={id}
                      onChange={(event) => setSelected(event.target.value)}
                    >
                      {list.data.map((repo) => (
                        <option key={repo.id} value={repo.id}>
                          {repo.name} ({repo.id})
                        </option>
                      ))}
                    </select>
                    {!activity && (
                      <ul className="space-y-2">
                        {list.data.map((repo) => (
                          <li key={repo.id}>
                            <button
                              type="button"
                              aria-pressed={id === repo.id}
                              className="w-full rounded border border-border p-3 text-left hover:bg-surface"
                              onClick={() => setSelected(repo.id)}
                            >
                              <span className="font-medium">{repo.name}</span>
                              <span className="block break-all text-sm">
                                {repo.id} · {repo.path}
                              </span>
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}
                  </>
                ) : (
                  <EmptyState
                    variant="empty"
                    title="No repositories found"
                    description="No local Git repositories were returned."
                  />
                )}
              </ReadRegion>
            ) : (
              activity && (
                <form
                  className="space-y-2"
                  onSubmit={(event) => {
                    event.preventDefault()
                    setSelected(input)
                  }}
                >
                  <Label htmlFor="scm-repository-id">Repository ID</Label>
                  <Input
                    id="scm-repository-id"
                    value={input}
                    onChange={(event) => setInput(event.target.value)}
                  />
                  <Button variant="outline" type="submit" disabled={!input}>
                    Read activity
                  </Button>
                </form>
              )
            )}
            {id &&
              (activity ? (
                <section className="space-y-3" aria-label="Commit activity">
                  <Label htmlFor="scm-history-limit">Commit limit</Label>
                  <select
                    id="scm-history-limit"
                    className={selectClass}
                    value={limit}
                    onChange={(event) => setLimit(Number(event.target.value))}
                  >
                    {[20, 50, 200].map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </select>
                  <ReadRegion {...history}>
                    {history.data && (
                      <>
                        <p>Local branches: {history.data.branches?.join(", ") || "None"}</p>
                        {history.data.commits?.length ? (
                          <ol className="space-y-3">
                            {history.data.commits.map((commit) => (
                              <li key={commit.hash} className="rounded border border-border p-3">
                                <h2 className="break-words font-medium">{commit.subject}</h2>
                                <p className="text-sm">
                                  {commit.author} ·{" "}
                                  {Number.isNaN(Date.parse(commit.timestamp))
                                    ? "Timestamp unavailable"
                                    : new Date(commit.timestamp).toLocaleString()}
                                </p>
                                <code className="block break-all text-xs">{commit.hash}</code>
                              </li>
                            ))}
                          </ol>
                        ) : (
                          <EmptyState
                            variant="empty"
                            title="No commits yet"
                            description="This repository returned no commit history."
                          />
                        )}
                      </>
                    )}
                  </ReadRegion>
                </section>
              ) : (
                <RepositoryDetail key={id} id={id} />
              ))}
          </>
        )}
      </div>
    </ListPageLayout>
  )
}
export function SCMRepositoriesPage() {
  return <SCMPage />
}
export function SCMActivityPage() {
  return <SCMPage activity />
}
