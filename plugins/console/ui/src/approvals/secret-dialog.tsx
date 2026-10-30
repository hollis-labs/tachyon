import { useEffect, useRef, useState } from "react"
import { Button, Dialog, DialogContent, DialogDescription, DialogTitle } from "@hollis-labs/design-components"
import { ApprovalError, type ApprovalClient, type SecretStored } from "./client"

const MAX_VALUE = 16384
const MAX_FILE = 65536

// The only way to fill a secret field. The value goes to the server once and comes back as a
// reference; it is held in this component only while the dialog is open and is dropped on
// success, cancel and close. It is never put in a form answer, a log or the DOM after storing.
export function SecretDialog({ client, itemId, field, label, onStored, onClose }: {
  client: ApprovalClient
  itemId: string
  field: string
  label: string
  onStored(stored: SecretStored): void
  onClose(): void
}) {
  const [value, setValue] = useState("")
  const [file, setFile] = useState<File | null>(null)
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [needsAccess, setNeedsAccess] = useState(false)
  // One key per dialog, so a retry after a step-up returns the same reference.
  const key = useRef(crypto.randomUUID())
  const live = useRef(true)
  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])
  const size = file ? file.size : new TextEncoder().encode(value).length
  const tooBig = file ? file.size > MAX_FILE : size > MAX_VALUE
  const ready = (file ? file.size > 0 : value.length > 0) && !tooBig
  const close = () => {
    setValue("")
    setFile(null)
    onClose()
  }
  async function store() {
    setBusy(true)
    setError("")
    setNeedsAccess(false)
    try {
      const stored = await client.storeSecret({ itemId, field, ...(file ? { file } : { value }), idempotencyKey: key.current })
      setValue("")
      setFile(null)
      onStored(stored)
    } catch (e) {
      if (!live.current) return
      if (e instanceof ApprovalError && e.code === "assurance_insufficient") setNeedsAccess(true)
      else if (e instanceof ApprovalError && e.code === "secrets_unavailable") setError("This server is not set up to store secrets. Ask an operator to configure it.")
      else if (e instanceof ApprovalError && e.code === "item_not_pending") setError("This request has already ended, so it can no longer take a secret.")
      else setError(e instanceof Error ? e.message : "Could not store the secret.")
    } finally {
      if (live.current) setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) close() }}>
      <DialogContent aria-label={`Provide ${label}`} className="sm:max-w-md">
        <DialogTitle>{label}</DialogTitle>
        <DialogDescription>Stored securely and never shown again. The agent cannot read it.</DialogDescription>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            if (ready && !busy) void store()
          }}
        >
          {!file && (
            <div>
            <label className="block dash-control text-fg-muted">
              Secret value
              <input
                type={reveal ? "text" : "password"}
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="off"
                autoFocus
                aria-label="Secret value"
                className="mt-1 w-full border border-border bg-bg p-2 font-mono dash-control text-fg"
                disabled={busy}
                value={value}
                onChange={(e) => setValue(e.target.value)}
              />
            </label>
            {/* Shown only while held; released, blurred or leaving hides it again. */}
            <button
              type="button"
              className="mt-1 dash-support text-fg-muted underline underline-offset-2 hover:text-fg"
              onPointerDown={() => setReveal(true)}
              onPointerUp={() => setReveal(false)}
              onPointerLeave={() => setReveal(false)}
              onBlur={() => setReveal(false)}
              onKeyDown={(e) => { if (e.key === " " || e.key === "Enter") setReveal(true) }}
              onKeyUp={() => setReveal(false)}
            >
              Hold to reveal
            </button>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2 dash-support text-fg-muted">
            {file ? (
              <>
                <span className="font-mono text-fg">{file.name}</span>
                <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={() => setFile(null)}>Remove file</Button>
              </>
            ) : (
              <label className="cursor-pointer underline underline-offset-2 hover:text-fg">
                Use a file instead
                <input type="file" aria-label="Secret file" className="sr-only" disabled={busy} onChange={(e) => { setFile(e.target.files?.[0] ?? null); setValue("") }} />
              </label>
            )}
          </div>
          {tooBig && <p role="alert" className="dash-support text-danger">Too large: values up to {MAX_VALUE.toLocaleString()} bytes, files up to {MAX_FILE.toLocaleString()} bytes.</p>}
          {needsAccess && (
            <p role="alert" className="border border-border px-3 py-2 dash-control text-fg">
              Operator access is needed to store a secret. Grant it, then try again; what you entered is kept until you close this.
            </p>
          )}
          {error && <p role="alert" className="border border-danger/40 px-3 py-2 dash-control text-danger">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={close}>Cancel</Button>
            <Button type="submit" disabled={!ready || busy}>{busy ? "Storing…" : needsAccess ? "Try again" : "Store secret"}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
