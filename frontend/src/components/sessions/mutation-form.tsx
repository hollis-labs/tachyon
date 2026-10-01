import { Button, Input, Label, Textarea } from "@hollis-labs/design-components"
import { useEffect, useRef, useState } from "react"
import { sessionsApi } from "../../api/sessions"
import { PendingApproval } from "../pending-approval"
import type { MutationFeedback } from "./read-state"

export function SessionMutationForm({
  action,
  id,
}: {
  action: "create" | "stop" | "submit"
  id?: string
}) {
  const [launch, setLaunch] = useState("")
  const [prompt, setPrompt] = useState("")
  const [key, setKey] = useState("")
  const [confirm, setConfirm] = useState(false)
  const [feedback, setFeedback] = useState<MutationFeedback>({ busy: false, message: "" })
  const actionTrigger = useRef<HTMLButtonElement | null>(null)
  const confirmTrigger = useRef<HTMLButtonElement | null>(null)
  const restoreFocus = useRef(false)
  useEffect(() => {
    // Restore after the dialog's focus guard has observed the removed confirmation.
    const frame = requestAnimationFrame(() => {
      if (confirm) confirmTrigger.current?.focus()
      else if (restoreFocus.current) {
        actionTrigger.current?.focus()
        restoreFocus.current = false
      }
    })
    return () => cancelAnimationFrame(frame)
  }, [confirm])
  const flight = useRef(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const locked = feedback.busy || !!feedback.ask
  async function perform() {
    if (flight.current || locked || !confirm) return
    flight.current = true
    setFeedback({ busy: true, message: "" })
    try {
      const result =
        action === "create"
          ? await sessionsApi.create({
              launch_id: launch.trim(),
              boot_prompt: prompt,
              idempotency_key: key || undefined,
            })
          : action === "stop"
            ? await sessionsApi.stop(id ?? "")
            : await sessionsApi.submit(id ?? "", prompt)
      if (!mounted.current) return
      if (result.status === "ask") setFeedback({ busy: false, message: "", ask: result.ask })
      else if (result.status === "error")
        setFeedback({
          busy: false,
          message: ["validation", "invalid_state"].includes(result.error.code)
            ? `Request rejected: ${result.error.message}`
            : `Completion unknown, do not retry blindly. Inspect session details and history before starting a new action. ${result.error.message}`,
        })
      else
        setFeedback({
          busy: false,
          message:
            action === "create"
              ? `Provider returned session ${"id" in result.data ? result.data.id : ""}. Inspect it before starting another action.`
              : action === "stop"
                ? "Provider acknowledged the stop request. Refresh session details to inspect its current state."
                : "Provider acknowledged the turn submission. This does not mean the turn has completed; inspect history.",
        })
    } catch {
      if (mounted.current)
        setFeedback({
          busy: false,
          message:
            "Completion unknown, do not retry blindly. Inspect session details and history before starting a new action.",
        })
    } finally {
      flight.current = false
      if (mounted.current) {
        restoreFocus.current = true
        setConfirm(false)
      }
    }
  }
  const label =
    action === "create" ? "Create session" : action === "stop" ? "Stop session" : "Send turn"
  const ready = action === "create" ? !!launch.trim() : action === "submit" ? !!prompt.trim() : !!id
  return (
    <section aria-label={label} className="min-w-0 space-y-3 rounded border border-border p-3">
      <h3 className="font-medium">{label}</h3>
      {action === "create" && (
        <>
          <Label htmlFor="session-launch-id">Existing provider launch ID</Label>
          <Input
            id="session-launch-id"
            value={launch}
            readOnly={locked || confirm}
            onChange={(e) => setLaunch(e.target.value)}
          />
          <Label htmlFor="session-idempotency">Idempotency key (optional)</Label>
          <Input
            id="session-idempotency"
            value={key}
            readOnly={locked || confirm}
            onChange={(e) => setKey(e.target.value)}
          />
          <p className="text-sm text-text-muted">
            Operator-supplied key, retained across actions. The Launches page uses a local
            launch-intent ID in its key; this form cannot infer that key from a provider launch ID.
          </p>
        </>
      )}
      {action !== "stop" && (
        <>
          <Label htmlFor={`session-${action}-text`}>
            {action === "create" ? "Boot prompt (optional)" : "Turn text"}
          </Label>
          <Textarea
            id={`session-${action}-text`}
            value={prompt}
            readOnly={locked || confirm}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </>
      )}
      {confirm ? (
        <section className="space-y-3" aria-label={`Confirm ${label.toLowerCase()}`}>
          <p className="break-words">
            {action === "create"
              ? `Ask the provider to allocate a session for launch ${launch.trim()}?`
              : action === "stop"
                ? `Stop session ${id}? This asks the provider to stop execution.`
                : `Send this turn to session ${id}? The provider may execute tools or other actions.`}
          </p>
          {action === "create" && (
            <p>
              If this launch already has a session (for example from the Launches page), this may
              create another one.
            </p>
          )}
          {prompt && (
            <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words text-xs">
              {prompt}
            </pre>
          )}
          {action === "submit" && (
            <p className="text-sm">
              Submission can take up to 30 seconds. A timeout does not prove that execution stopped.
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              ref={confirmTrigger}
              aria-disabled={locked}
              className="aria-disabled:opacity-50"
              onClick={() => void perform()}
            >
              Confirm {label.toLowerCase()}
            </Button>
            <Button
              variant="outline"
              disabled={locked}
              onClick={() => {
                restoreFocus.current = true
                setConfirm(false)
              }}
            >
              Cancel
            </Button>
          </div>
        </section>
      ) : (
        <Button
          ref={actionTrigger}
          aria-disabled={locked || !ready}
          className="aria-disabled:opacity-50"
          onClick={() => {
            if (!locked && ready) {
              setFeedback({ busy: false, message: "" })
              setConfirm(true)
            }
          }}
        >
          {label}
        </Button>
      )}
      {feedback.busy && <p role="status">Request in progress. Do not submit it again.</p>}
      {feedback.message && (
        <p role="status" className="break-words text-sm">
          {feedback.message}
        </p>
      )}
      {feedback.ask && (
        <>
          <PendingApproval ask={feedback.ask} />
          <p className="text-sm">
            Starting a new action clears this local notice; it does not continue or replay the
            requested operation.
          </p>
          <Button
            variant="outline"
            onClick={() => {
              setFeedback({ busy: false, message: "" })
              actionTrigger.current?.focus()
            }}
          >
            Start a new action
          </Button>
        </>
      )}
    </section>
  )
}
