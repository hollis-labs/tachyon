import { Button, Label, Skeleton, Textarea } from "@hollis-labs/design-components"
import { useEffect, useState } from "react"
import type { Agent, WorkItem } from "../../api/client"
import { useApi } from "../../api/context"
import { PendingApprovalError } from "../../api/hitl"
import type { AskDetail, Envelope, useVerbs } from "../../api/verbs"
import { LargeDialog } from "../agent-ops/large-dialog"
import { PendingApproval } from "../pending-approval"

const selectStyle = "h-8 rounded-md border border-border bg-surface px-2 text-sm text-text"
const torqueStatuses = [
  "backlog",
  "todo",
  "queued",
  "doing",
  "review",
  "done",
  "blocked",
  "paused",
  "archived",
  "abandoned",
  "cancelled",
]
const message = (error: unknown) => (error instanceof Error ? error.message : String(error))
export const assigneeOf = (task: WorkItem) =>
  typeof task.metadata?.assignee === "string" ? task.metadata.assignee : ""
type Capabilities = ReturnType<typeof useVerbs>

export function dataOf<T>(result: Envelope<T>): T {
  if (result.status === "ok") return result.data
  if (result.status === "error") throw new Error(result.error.message)
  throw new PendingApprovalError(result.ask)
}

export function WorkDetail({
  id,
  agentError,
  verbs,
  agents,
  onClose,
  onUpdated,
}: {
  id: string
  verbs: Capabilities
  agents: Agent[]
  agentError: string
  onClose: () => void
  onUpdated: () => void
}) {
  const api = useApi()
  const [task, setTask] = useState<WorkItem | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [assignee, setAssignee] = useState("")
  const [status, setStatus] = useState("")
  const [comment, setComment] = useState("")
  const [notice, setNotice] = useState("")
  const [pending, setPending] = useState<AskDetail | null>(null)
  const canRead = verbs.has("work_read")
  useEffect(() => {
    setTask(null)
    setError("")
    setPending(null)
    if (!canRead) {
      setError("Task details are unavailable from the connected provider.")
      return
    }
    let active = true
    api
      .readWork(id)
      .then((result) => {
        if (active) {
          const item = dataOf(result)
          setTask(item)
          setAssignee(assigneeOf(item))
          setStatus(item.status)
        }
      })
      .catch((error) => {
        if (active) {
          if (error instanceof PendingApprovalError) setPending(error.ask)
          else setError(message(error))
        }
      })
    return () => {
      active = false
    }
  }, [api, id, canRead])

  async function mutate(action: "assign" | "transition" | "comment") {
    if (
      busy ||
      pending ||
      (action === "assign" && (!assignee || assignee === (task ? assigneeOf(task) : ""))) ||
      (action === "transition" && status === task?.status) ||
      (action === "comment" && !comment.trim())
    )
      return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      if (action === "comment") {
        dataOf(await api.commentWork(id, comment.trim()))
        setComment("")
        setNotice("Comment added.")
      } else {
        const updated = dataOf(
          action === "assign"
            ? await api.assignWork(id, assignee)
            : await api.transitionWork(id, status),
        )
        setTask(updated)
        setAssignee(assigneeOf(updated))
        setStatus(updated.status)
        setNotice(action === "assign" ? "Assignment updated." : "Status updated.")
      }
      onUpdated()
    } catch (error) {
      if (error instanceof PendingApprovalError) setPending(error.ask)
      else setError(message(error))
    } finally {
      setBusy(false)
    }
  }

  const currentAssignee = task ? assigneeOf(task) : ""
  const agentChoices = agents.filter((agent) => agent.status !== "disabled")
  const canAssign = verbs.has("work_assign") && verbs.has("agent_list")
  return (
    <LargeDialog
      open
      onClose={onClose}
      title={task?.title ?? "Task details"}
      description={id}
      footer={
        <Button type="button" variant="outline" onClick={onClose}>
          Close
        </Button>
      }
    >
      {error && (
        <p role="alert" className="mb-4 text-sm text-status-failed">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="mb-4 text-sm text-status-done">
          {notice}
        </p>
      )}
      {pending && <PendingApproval ask={pending} />}
      {!task ? (
        !error && !pending && <Skeleton className="h-40 w-full" />
      ) : (
        <div className="space-y-6">
          <div className="flex flex-wrap gap-4 text-sm text-text-soft">
            <span>Status: {task.status}</span>
            <span>Priority: {task.priority}</span>
            <span>
              Assignee:{" "}
              {agents.find((agent) => agent.id === currentAssignee)?.name ??
                (currentAssignee || "Unassigned")}
            </span>
            {task.project_id && <span>Project: {task.project_id}</span>}
          </div>
          <p className="whitespace-pre-wrap text-sm">
            {task.description || "No description provided."}
          </p>
          {canAssign && (
            <div className="space-y-2">
              <Label htmlFor="work-detail-assignee">Assign agent</Label>
              <div className="flex flex-wrap gap-2">
                <select
                  id="work-detail-assignee"
                  className={selectStyle}
                  value={assignee}
                  disabled={busy || !!pending}
                  onChange={(event) => setAssignee(event.target.value)}
                >
                  <option value="">Select an agent</option>
                  {currentAssignee &&
                    !agentChoices.some((agent) => agent.id === currentAssignee) && (
                      <option value={currentAssignee}>{currentAssignee}</option>
                    )}
                  {agentChoices.map((agent) => (
                    <option key={agent.id} value={agent.id}>
                      {agent.name}
                    </option>
                  ))}
                </select>
                <Button
                  className="aria-disabled:opacity-50"
                  type="button"
                  size="sm"
                  aria-disabled={busy || !!pending || !assignee || assignee === currentAssignee}
                  onClick={() => mutate("assign")}
                >
                  Assign
                </Button>
              </div>
              {agentChoices.length === 0 && (
                <p className="text-xs text-text-subtle">
                  {agentError || "No agents are available for assignment."}
                </p>
              )}
            </div>
          )}
          {verbs.has("work_transition") && (
            <div className="space-y-2">
              <Label htmlFor="work-detail-status">Change status</Label>
              <div className="flex flex-wrap gap-2">
                <select
                  id="work-detail-status"
                  className={selectStyle}
                  value={status}
                  disabled={busy || !!pending}
                  onChange={(event) => setStatus(event.target.value)}
                >
                  {Array.from(new Set([...torqueStatuses, task.status])).map((value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  ))}
                </select>
                <Button
                  className="aria-disabled:opacity-50"
                  type="button"
                  size="sm"
                  aria-disabled={busy || !!pending || status === task.status}
                  onClick={() => mutate("transition")}
                >
                  Update status
                </Button>
              </div>
            </div>
          )}
          {verbs.has("work_comment") && (
            <div className="space-y-2">
              <Label htmlFor="work-detail-comment">Comment</Label>
              <Textarea
                id="work-detail-comment"
                placeholder="Add a comment"
                value={comment}
                disabled={busy || !!pending}
                onChange={(event) => setComment(event.target.value)}
              />
              <Button
                className="aria-disabled:opacity-50"
                type="button"
                size="sm"
                aria-disabled={busy || !!pending || !comment.trim()}
                onClick={() => mutate("comment")}
              >
                Add comment
              </Button>
            </div>
          )}
        </div>
      )}
    </LargeDialog>
  )
}
