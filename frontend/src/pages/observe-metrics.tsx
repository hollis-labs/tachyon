import { Button, EmptyState } from "@hollis-labs/design-components"
import { useCallback, useState } from "react"
import { type MetricFilter, observeApi, observeData } from "../api/observe"
import { useVerbs } from "../api/verbs"
import { snapshotRows } from "../components/observe/observe-kit-adapter"
import {
  dateFilters,
  FilterInput,
  keyedSnapshot,
  limitValue,
  MetricSample,
  ObserveLayout,
  ReadControls,
  ReadRegion,
  timeText,
  useObserveRead,
} from "../components/observe/observe-shared"

export function ObserveMetricsPage() {
  const verbs = useVerbs()
  const [names, setNames] = useState("")
  const [tagKey, setTagKey] = useState("")
  const [tagValue, setTagValue] = useState("")
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const [limit, setLimit] = useState("100")
  const [filter, setFilter] = useState<MetricFilter>({ limit: 100 })
  const [validation, setValidation] = useState("")
  const load = useCallback(
    () =>
      observeApi
        .metrics(filter)
        .then(observeData)
        .then((rows) => snapshotRows(rows, ["name", "timestamp"])),
    [filter],
  )
  const read = useObserveRead(verbs.has("observe_metrics"), load)
  return (
    <ObserveLayout title="Observe Metrics" verb="observe_metrics">
      <p className="text-sm">
        Direct reads of retained samples; no continuous history, tokens, cost, or OTel series
        promised. Missing measurements are unavailable, never zero.
      </p>
      <form
        className="grid items-end gap-3 sm:grid-cols-3"
        onSubmit={(event) => {
          event.preventDefault()
          if (read.ask) return
          try {
            if (tagValue && !tagKey) throw new Error("Enter a tag key for the value.")
            const exactNames = names
              .split(",")
              .map((name) => name.trim())
              .filter(Boolean)
            setFilter({
              ...(exactNames.length ? { names: exactNames } : {}),
              ...(tagKey ? { tags: { [tagKey]: tagValue } } : {}),
              ...dateFilters(since, until),
              limit: limitValue(limit),
            })
            setValidation("")
          } catch (error) {
            setValidation(error instanceof Error ? error.message : String(error))
          }
        }}
      >
        <FilterInput
          name="Names (comma-separated)"
          value={names}
          change={setNames}
          disabled={!!read.ask}
        />
        <FilterInput name="Tag key" value={tagKey} change={setTagKey} disabled={!!read.ask} />
        <FilterInput name="Tag value" value={tagValue} change={setTagValue} disabled={!!read.ask} />
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
          <ul className="space-y-3">
            {keyedSnapshot(read.data).map(({ row: point, key }) => (
              <li
                key={key}
                className="min-w-0 space-y-2 rounded border border-border p-4 break-words"
              >
                <MetricSample label={point.name} at={point.timestamp}>
                  <h2 className="font-medium">{point.name}</h2>
                  <p>
                    {Number.isFinite(point.value) ? point.value : "Unavailable"} ·{" "}
                    {point.unit || "Unit not supplied"}
                  </p>
                  <p className="text-sm">{timeText(point.timestamp)}</p>
                  {point.tags && (
                    <dl className="grid grid-cols-1 gap-1 text-sm sm:grid-cols-2">
                      {Object.entries(point.tags).map(([key, value]) => (
                        <div key={key}>
                          <dt className="text-text-muted">{key}</dt>
                          <dd className="break-all">{value}</dd>
                        </div>
                      ))}
                    </dl>
                  )}
                </MetricSample>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState
            variant="empty"
            title="No retained metric samples match these filters"
            description="An empty snapshot is not a zero measurement."
          />
        )}
      </ReadRegion>
    </ObserveLayout>
  )
}
