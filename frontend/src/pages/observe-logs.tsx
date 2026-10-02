import { Button, EmptyState, Label } from "@hollis-labs/design-components"
import { useCallback, useState } from "react"
import { type LogFilter, observeApi, observeData } from "../api/observe"
import { useVerbs } from "../api/verbs"
import { snapshotRows } from "../components/observe/observe-kit-adapter"
import {
  Details,
  dateFilters,
  FilterInput,
  keyedSnapshot,
  limitValue,
  ObserveLayout,
  ReadControls,
  ReadRegion,
  selectClass,
  timeText,
  useObserveRead,
} from "../components/observe/observe-shared"

export function ObserveLogsPage() {
  const verbs = useVerbs()
  const [source, setSource] = useState("")
  const [level, setLevel] = useState("")
  const [search, setSearch] = useState("")
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const [limit, setLimit] = useState("100")
  const [filter, setFilter] = useState<LogFilter>({ limit: 100 })
  const [validation, setValidation] = useState("")
  const load = useCallback(
    () =>
      observeApi
        .logs(filter)
        .then(observeData)
        .then((rows) => snapshotRows(rows, ["id", "timestamp", "level", "source", "message"])),
    [filter],
  )
  const read = useObserveRead(verbs.has("observe_logs"), load, {
    channel: "logs",
    filter,
    enabled: verbs.has("observe_subscribe"),
  })
  return (
    <ObserveLayout title="Observe Logs" verb="observe_logs">
      <p className="text-sm">
        Local and host-recorded logs only; does not cover every provider, model output, or
        conversation.
      </p>
      <form
        className="grid items-end gap-3 sm:grid-cols-3"
        onSubmit={(event) => {
          event.preventDefault()
          if (read.ask) return
          try {
            setFilter({
              ...(source ? { source } : {}),
              ...(level ? { level } : {}),
              ...(search ? { search } : {}),
              ...dateFilters(since, until),
              limit: limitValue(limit),
            })
            setValidation("")
          } catch (error) {
            setValidation(error instanceof Error ? error.message : String(error))
          }
        }}
      >
        <FilterInput name="Source" value={source} change={setSource} disabled={!!read.ask} />
        <div>
          <Label htmlFor="observe-level">Level</Label>
          <select
            id="observe-level"
            className={`${selectClass} w-full`}
            value={level}
            disabled={!!read.ask}
            onChange={(event) => setLevel(event.target.value)}
          >
            {["", "debug", "info", "warn", "error"].map((item) => (
              <option key={item} value={item}>
                {item || "All"}
              </option>
            ))}
          </select>
        </div>
        <FilterInput
          name="Message contains (case-sensitive)"
          value={search}
          change={setSearch}
          disabled={!!read.ask}
        />
        <FilterInput
          name="From"
          value={since}
          change={setSince}
          type="datetime-local"
          disabled={!!read.ask}
        />
        <FilterInput
          name="Until"
          value={until}
          change={setUntil}
          type="datetime-local"
          disabled={!!read.ask}
        />
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
      <ReadRegion read={read}>
        {read.data?.length ? (
          <ol className="space-y-3">
            {keyedSnapshot(read.data).map(({ row: entry, key }) => (
              <li
                key={key}
                className="min-w-0 space-y-2 rounded border border-border p-4 break-words"
              >
                <p className="text-sm">
                  {timeText(entry.timestamp)} · {entry.level} · {entry.source}
                </p>
                <p className="whitespace-pre-wrap break-words">{entry.message}</p>
                <Details value={entry.fields} />
              </li>
            ))}
          </ol>
        ) : (
          <EmptyState
            variant="empty"
            title="No retained logs match these filters"
            description="This bounded snapshot does not describe failures outside the retained logs."
          />
        )}
      </ReadRegion>
    </ObserveLayout>
  )
}
