import { Button, Input, Label } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useState } from "react"
import { useVerbs } from "../api/verbs"
import { SessionHistoryPanel } from "../components/sessions/history-panel"

export function SessionHistoryPage() {
  const verbs = useVerbs()
  const [draft, setDraft] = useState("")
  const [since, setSince] = useState("0")
  const [selection, setSelection] = useState<{ id: string; seq: number; revision: number } | null>(
    null,
  )
  const valid = draft.trim() && Number.isSafeInteger(Number(since)) && Number(since) >= 0
  return (
    <div className="min-w-0 space-y-4">
      <PageHeader title="Session History" />
      <div className="min-w-0 space-y-4 p-4">
        <p className="text-sm text-text-muted">
          Tether-backed provider session events. Durable agent sessions are a separate Nanite
          surface.
        </p>
        {verbs.loading ? (
          <p role="status">Discovering session capabilities…</p>
        ) : !verbs.has("session_history") ? (
          <p>Session history is unavailable.</p>
        ) : (
          <>
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(e) => {
                e.preventDefault()
                if (valid)
                  setSelection({
                    id: draft.trim(),
                    seq: Number(since),
                    revision: (selection?.revision ?? 0) + 1,
                  })
              }}
            >
              <div className="min-w-0 space-y-2">
                <Label htmlFor="history-session-id">Session ID</Label>
                <Input
                  id="history-session-id"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                />
              </div>
              <div className="min-w-0 space-y-2">
                <Label htmlFor="history-since-seq">Since sequence</Label>
                <Input
                  id="history-since-seq"
                  type="number"
                  min={0}
                  value={since}
                  onChange={(e) => setSince(e.target.value)}
                />
              </div>
              <Button type="submit" disabled={!valid}>
                Load history
              </Button>
            </form>
            {selection ? (
              <SessionHistoryPanel
                key={selection.revision}
                id={selection.id}
                sinceSeq={selection.seq}
              />
            ) : (
              <p>Enter a provider session ID to inspect its event history.</p>
            )}
          </>
        )}
      </div>
    </div>
  )
}
