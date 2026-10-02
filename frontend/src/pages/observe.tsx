import { Button, EmptyState } from "@hollis-labs/design-components"
import { useCallback, useState } from "react"
import {
  type ActivityEntry,
  type ActivityFilter,
  type ObserveEvent,
  observeApi,
  observeData,
} from "../api/observe"
import { useVerbs } from "../api/verbs"
import { snapshotRows } from "../components/observe/observe-kit-adapter"
import {
  Details,
  FilterInput,
  keyedSnapshot,
  limitValue,
  ObserveLayout,
  ObserveStatus,
  ReadControls,
  ReadRegion,
  timeText,
  useObserveRead,
} from "../components/observe/observe-shared"

function Feed({ channel }: { channel: "activity" | "events" }) {
  const verbs = useVerbs()
  const [source, setSource] = useState("")
  const [kind, setKind] = useState("")
  const [limit, setLimit] = useState("50")
  const [filter, setFilter] = useState<ActivityFilter>({ limit: 50 })
  const [validation, setValidation] = useState("")
  const load = useCallback(
    () =>
      observeApi
        .snapshot<(ActivityEntry | ObserveEvent)[] | null>(channel, { ...filter })
        .then(observeData)
        .then((rows) =>
          snapshotRows(rows, [
            "id",
            "timestamp",
            "kind",
            "source",
            ...(channel === "activity" ? ["summary", "actor"] : []),
          ]),
        ),
    [channel, filter],
  )
  const read = useObserveRead(verbs.has(`observe_${channel}`), load, {
    channel,
    filter,
    enabled: verbs.has("observe_subscribe"),
  })
  return (
    <section
      className="space-y-4"
      aria-label={channel === "activity" ? "Activity snapshots" : "Event snapshots"}
    >
      <form
        className="grid items-end gap-3 sm:grid-cols-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (read.ask) return
          try {
            setFilter({
              ...(source ? { source } : {}),
              ...(kind ? { kind } : {}),
              limit: limitValue(limit),
            })
            setValidation("")
          } catch (error) {
            setValidation(error instanceof Error ? error.message : String(error))
          }
        }}
      >
        <FilterInput name="Source" value={source} change={setSource} disabled={!!read.ask} />
        <FilterInput name="Kind" value={kind} change={setKind} disabled={!!read.ask} />
        <FilterInput
          name="Limit"
          value={limit}
          change={setLimit}
          type="number"
          disabled={!!read.ask}
        />
        <Button variant="outline" type="submit" disabled={!!read.ask}>
          Apply filters
        </Button>
      </form>
      {validation && (
        <p role="alert" className="text-status-failed">
          {validation}
        </p>
      )}
      <ReadControls read={read} />
      <p className="text-sm text-text-muted">
        Exact source and kind filters. Dependency probes and session snapshots are current
        observations, not transitions. Repeated reads replace this bounded snapshot.
      </p>
      <ReadRegion read={read}>
        {read.data?.length ? (
          <ol className="space-y-3">
            {keyedSnapshot(read.data).map(({ row: entry, key }) => (
              <li
                key={key}
                className="min-w-0 space-y-2 rounded border border-border p-4 break-words"
              >
                <h2 className="font-medium">{"summary" in entry ? entry.summary : entry.kind}</h2>
                <p className="text-sm">
                  {timeText(entry.timestamp)} · {entry.source} · {entry.kind}
                  {"actor" in entry && entry.actor ? ` · ${entry.actor}` : ""}
                </p>
                <p className="break-all text-xs text-text-muted">{entry.id}</p>
                <Details value={"summary" in entry ? entry.detail : entry.payload} />
              </li>
            ))}
          </ol>
        ) : (
          <EmptyState
            variant="empty"
            title={
              channel === "activity" ? "No activity in this snapshot" : "No events in this snapshot"
            }
            description="No retained records or current observations match these filters."
          />
        )}
      </ReadRegion>
    </section>
  )
}
export function ObservePage() {
  const verbs = useVerbs()
  const [tab, setTab] = useState<"activity" | "events">("activity")
  const selected = tab === "events" && verbs.has("observe_events") ? "events" : "activity"
  return (
    <ObserveLayout title="Observe Activity" verb="observe_activity">
      {verbs.has("observe_events") && (
        <fieldset className="flex gap-2" aria-label="Observation views">
          <Button
            variant="outline"
            aria-pressed={selected === "activity"}
            onClick={() => setTab("activity")}
          >
            Activity
          </Button>
          <Button
            variant="outline"
            aria-pressed={selected === "events"}
            onClick={() => setTab("events")}
          >
            Events
          </Button>
        </fieldset>
      )}
      <Feed key={selected} channel={selected} />
      <ObserveStatus />
    </ObserveLayout>
  )
}
