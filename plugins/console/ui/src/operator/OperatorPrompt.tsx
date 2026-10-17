import { useEffect, useState, useSyncExternalStore } from "react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@hollis-labs/design-components"
import { KeyRound } from "lucide-react"
import { operatorElevation } from "./elevation"

export function OperatorPrompt() {
  const state = useSyncExternalStore(
    operatorElevation.subscribe,
    operatorElevation.snapshot,
  )
  const [key, setKey] = useState("")
  useEffect(() => {
    if (!state.open) setKey("")
  }, [state.open])
  return (
    <Dialog
      open={state.open}
      onOpenChange={(open) => {
        if (!open && !state.submitting) operatorElevation.cancel()
      }}
    >
      <DialogContent className="z-[100] sm:max-w-md">
        <DialogHeader>
          <div className="flex items-center gap-3">
            <KeyRound className="h-6 w-6 text-amber-300" />
            <DialogTitle className="font-mono text-sm uppercase tracking-wide">
              Operator access
            </DialogTitle>
          </div>
          <DialogDescription className="text-xs">
            Enter your operator key to continue. Access lasts up to 12 hours;
            the key is sent once and isn’t stored by Tachyon.
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            const value = key
            setKey("")
            void operatorElevation.submit(value)
          }}
        >
          <label className="block text-xs text-zinc-400">
            Operator key
            <input
              type="password"
              aria-label="Operator key"
              autoComplete="off"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              disabled={state.submitting}
              className="mt-2 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-2 font-mono text-sm text-zinc-200"
            />
          </label>
          {state.error && (
            <p role="alert" className="text-xs text-red-300">
              {state.error}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <button
              type="button"
              disabled={state.submitting}
              onClick={() => operatorElevation.cancel()}
              className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-400 disabled:opacity-40"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!key || state.submitting}
              className="rounded border border-amber-400/40 bg-amber-950/20 px-3 py-1.5 text-xs text-amber-300 disabled:opacity-40"
            >
              {state.submitting ? "Verifying…" : "Unlock for 12 hours"}
            </button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
