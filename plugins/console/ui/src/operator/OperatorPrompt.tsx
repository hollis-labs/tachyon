import { useEffect, useState, useSyncExternalStore } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@hollis-labs/design-components"
import { operatorElevation } from "./elevation"

// Golden: tachyon-settings-golden-page@1, sign-in strip (Sign in | Confirm | Wrong | Locked).
// Until auth hardening (CW-20261030-0082) the only method is the operator key;
// passkey, remember and attempts-left light up from the same fields when present.
function useCountdown(until?: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!until) return
    setNow(Date.now())
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [until])
  if (!until) return 0
  return Math.max(0, Math.ceil((until - now) / 1000))
}
const clock = (s: number) =>
  `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`

const cap = "font-mono text-label uppercase tracking-wide text-fg-muted"
const field =
  "w-full rounded-control border border-border bg-bg-elevated px-2.5 py-2 text-xs text-fg disabled:opacity-50"

export function OperatorPrompt() {
  const state = useSyncExternalStore(
    operatorElevation.subscribe,
    operatorElevation.snapshot,
  )
  const [key, setKey] = useState("")
  const [remember, setRemember] = useState(false)
  useEffect(() => {
    if (!state.open) {
      setKey("")
      setRemember(false)
    }
  }, [state.open])
  const left = useCountdown(state.lockedUntil)
  const locked = !!state.lockedUntil && left > 0
  const methods = state.methods
  const step = state.stepUp
  const wrong = !!state.error && !locked
  const canRemember = !!methods?.includes("remember")
  const close = () => {
    if (!state.submitting) operatorElevation.cancel()
  }
  const submit = () => {
    const value = key
    setKey("")
    void operatorElevation.submit(value)
  }
  const title = locked
    ? "Too many attempts"
    : step
      ? "Confirm it is you"
      : "Sign in to Tachyon"
  return (
    <Dialog open={state.open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="z-[100] flex w-[min(440px,calc(100vw-32px))] flex-col gap-3 rounded-panel border border-surface-hover bg-bg-elevated p-4 sm:max-w-[440px]">
        <DialogTitle className="text-[15px] font-semibold text-fg">{title}</DialogTitle>
        {locked ? (
          <>
            <div
              role="alert"
              className="rounded-panel border border-danger/35 px-3 py-2.5 text-xs text-danger"
            >
              <b>Sign-in is paused.</b> Try again in about{" "}
              <span className="font-mono">{clock(left)}</span>. Your drafts are kept.
            </div>
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={close}>
                Close
              </Button>
            </div>
          </>
        ) : (
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (key) submit()
            }}
          >
            <DialogDescription className="text-xs text-fg-muted">
              {step
                ? "This action changes something that is hard to undo."
                : "Sign in to continue. Your drafts and queued comments are kept."}{" "}
              {operatorElevation.lifetimeCopy()}
            </DialogDescription>
            {step && (step.summary || step.action) && (
              <div className="overflow-hidden rounded-panel border border-border">
                <div className="flex items-center gap-2.5 border-b border-border-subtle px-3 py-2 text-xs">
                  <span className="font-mono">{step.summary ?? step.action}</span>
                  <span className="flex-1" />
                  {step.risk && (
                    <span className="rounded-sm border border-border px-1.5 py-0.5 font-mono text-label uppercase text-danger">
                      {step.risk} risk
                    </span>
                  )}
                </div>
                {step.summary && step.action && (
                  <div className="px-3 py-2.5 text-xs text-fg-muted">{step.action}</div>
                )}
              </div>
            )}
            <label className="flex flex-col gap-1.5">
              <span className={cap}>Operator key</span>
              <input
                type="password"
                aria-label="Operator key"
                aria-invalid={wrong || undefined}
                autoComplete="off"
                autoFocus
                value={key}
                onChange={(e) => setKey(e.target.value)}
                disabled={state.submitting}
                className={`${field} font-mono ${wrong ? "border-danger" : ""}`}
              />
              {wrong && (
                <span role="alert" className="text-label text-danger">
                  {state.error}
                  {typeof state.attemptsLeft === "number" &&
                    ` ${state.attemptsLeft} ${state.attemptsLeft === 1 ? "attempt" : "attempts"} left.`}
                </span>
              )}
            </label>
            {canRemember && (
              <label className="flex items-center gap-2 text-xs text-fg-secondary">
                <input
                  type="checkbox"
                  checked={remember}
                  onChange={(e) => setRemember(e.target.checked)}
                />
                Do not ask again for 5 minutes
              </label>
            )}
            <div className="flex items-center gap-2">
              {methods?.includes("passkey") && (
                <Button type="button" variant="outline" size="sm" className="flex-1">
                  Use a passkey
                </Button>
              )}
              <span className="flex-1" />
              {step && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={state.submitting}
                  onClick={close}
                >
                  Cancel
                </Button>
              )}
              <Button type="submit" size="sm" disabled={!key || state.submitting}>
                {state.submitting
                  ? "Verifying…"
                  : wrong
                    ? "Try again"
                    : step
                      ? "Confirm"
                      : "Sign in"}
              </Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
