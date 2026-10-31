import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Skeleton,
  Switch,
} from "@hollis-labs/design-components"
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ConflictError,
  type FieldResult,
  type GeneralApi,
  type SettingField,
  type SettingGroup,
} from "./general"
import { SetCard, SettingsColumn } from "./parts"

const INTRO = "Defaults for how Tachyon behaves. Each setting shows where its value comes from."
const SOURCE: Record<string, string> = {
  default: "Default",
  override: "Overridden",
  env: "Environment",
  file: "From file",
}

type Load =
  | { status: "loading" }
  | { status: "error"; reason: string }
  | { status: "ready"; groups: readonly SettingGroup[] }

interface Outcome {
  ok: number
  failed: { key: string; label: string; error: string }[]
}

export function GeneralSection({ api }: { api: GeneralApi }) {
  const [load, setLoad] = useState<Load>({ status: "loading" })
  // Draft is kept across reloads so a conflict never loses the user's edits.
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [review, setReview] = useState(false)
  const [saving, setSaving] = useState(false)
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  const [conflict, setConflict] = useState(false)

  const fetchGroups = useCallback(async () => {
    try {
      setLoad({ status: "ready", groups: await api.load() })
    } catch (e) {
      setLoad({
        status: "error",
        reason: e instanceof Error ? e.message : "Could not load settings.",
      })
    }
  }, [api])

  useEffect(() => {
    void fetchGroups()
  }, [fetchGroups])

  const groups = load.status === "ready" ? load.groups : []
  const fields = useMemo(() => groups.flatMap((g) => g.fields), [groups])
  // Only keys that still differ from the loaded value count as changes.
  const changes = useMemo(
    () => fields.filter((f) => f.key in draft && draft[f.key] !== f.value && !f.readOnlyReason),
    [fields, draft],
  )

  const edit = (f: SettingField, value: string) => {
    setOutcome(null)
    setDraft((d) => {
      const next = { ...d }
      if (value === f.value) delete next[f.key]
      else next[f.key] = value
      return next
    })
  }

  const save = async () => {
    setSaving(true)
    setConflict(false)
    const total: Outcome = { ok: 0, failed: [] }
    try {
      for (const g of groups) {
        const set = Object.fromEntries(
          g.fields.filter((f) => changes.includes(f)).map((f) => [f.key, draft[f.key]]),
        )
        if (Object.keys(set).length === 0) continue
        const { results } = await api.save(g, set)
        for (const [key, r] of Object.entries(results as Record<string, FieldResult>)) {
          if (r === "ok") total.ok++
          else
            total.failed.push({
              key,
              label: fields.find((f) => f.key === key)?.label ?? key,
              error: r.error,
            })
        }
      }
      setOutcome(total)
      // Failed fields keep their draft; saved ones are cleared.
      setDraft((d) =>
        Object.fromEntries(
          Object.entries(d).filter(([k]) => total.failed.some((f) => f.key === k)),
        ),
      )
      await fetchGroups()
    } catch (e) {
      if (e instanceof ConflictError) setConflict(true)
      else
        total.failed.push({
          key: "",
          label: "Save",
          error: e instanceof Error ? e.message : "Save failed",
        })
      if (!(e instanceof ConflictError)) setOutcome(total)
    } finally {
      setSaving(false)
      setReview(false)
    }
  }

  const reload = async () => {
    setConflict(false)
    await fetchGroups()
  }

  return (
    <SettingsColumn id="settings-general-h" title="General" intro={INTRO}>
      {load.status === "loading" && (
        <div
          role="status"
          aria-busy="true"
          aria-label="Loading settings"
          className="flex flex-col gap-3.5"
        >
          <SetCard>
            {[0, 1, 2].map((i) => (
              <div
                key={i}
                className="flex items-center justify-between gap-4 border-t border-border-subtle px-4 py-3 first:border-t-0"
              >
                <div className="flex flex-col gap-2">
                  <Skeleton className="h-3.5 w-32" />
                  <Skeleton className="h-3 w-56" />
                </div>
                <Skeleton className="h-7 w-32" />
              </div>
            ))}
          </SetCard>
        </div>
      )}
      {load.status === "error" && (
        <SetCard>
          <div className="flex flex-wrap items-center justify-between gap-4 px-4 py-3.5">
            <div>
              <p className="font-bold text-fg">Settings are not available</p>
              <p className="text-xs leading-normal text-fg-muted">
                {load.reason} Nothing here has been changed.
              </p>
            </div>
            <Button type="button" variant="outline" onClick={() => void fetchGroups()}>
              Try again
            </Button>
          </div>
        </SetCard>
      )}
      {load.status === "ready" &&
        groups.map((g) => (
          <SetCard key={g.id} label={groups.length > 1 ? g.label : undefined}>
            {g.fields.map((f) => (
              <FieldRow
                key={f.key}
                field={f}
                value={draft[f.key] ?? f.value}
                disabled={saving}
                onChange={(v) => edit(f, v)}
              />
            ))}
          </SetCard>
        ))}
      <div className="sticky bottom-0 flex flex-col gap-2 empty:hidden">
        {conflict && (
          <Bar role="alert" tone="danger">
            <span>
              <b>This changed elsewhere.</b>{" "}
              <span className="text-fg-muted">Reload and re-apply. Your edits are kept.</span>
            </span>
            <Button type="button" variant="outline" onClick={() => void reload()}>
              Reload
            </Button>
          </Bar>
        )}
        {outcome && changes.length === 0 && (
          <Bar role="status" tone={outcome.failed.length ? "danger" : "ok"}>
            <span>
              <b>
                Saved {outcome.ok} of {outcome.ok + outcome.failed.length}
                {outcome.failed.length ? `, ${outcome.failed.length} failed` : ""}
              </b>{" "}
              <span className="text-fg-muted">
                {outcome.failed.map((f) => `${f.label}: ${f.error}`).join(" ")}
              </span>
            </span>
            <Button type="button" variant="outline" onClick={() => setOutcome(null)}>
              Dismiss
            </Button>
          </Bar>
        )}
        {outcome && changes.length > 0 && outcome.failed.length > 0 && (
          <Bar role="status" tone="danger">
            <span>
              <b>
                Saved {outcome.ok} of {outcome.ok + outcome.failed.length}, {outcome.failed.length}{" "}
                failed
              </b>{" "}
              <span className="text-fg-muted">
                {outcome.failed.map((f) => `${f.label}: ${f.error}`).join(" ")}
              </span>
            </span>
          </Bar>
        )}
        {changes.length > 0 && (
          <Bar>
            <span className="min-w-0">
              <b>
                {changes.length} {changes.length === 1 ? "change" : "changes"}
              </b>{" "}
              <span className="text-fg-muted">
                {changes.length === 1
                  ? describe(changes[0], draft[changes[0].key])
                  : changes.map((c) => c.label).join(", ")}
              </span>
            </span>
            <span className="flex shrink-0 gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => setDraft({})}
              >
                Discard
              </Button>
              <Button
                type="button"
                size="sm"
                disabled={saving || conflict}
                onClick={() => setReview(true)}
              >
                Review and save
              </Button>
            </span>
          </Bar>
        )}
      </div>
      <Dialog open={review} onOpenChange={(o) => !saving && setReview(o)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Review changes</DialogTitle>
            <DialogDescription>These apply to this Tachyon, not just to you.</DialogDescription>
          </DialogHeader>
          <ul className="flex flex-col gap-2 text-xs">
            {changes.map((c) => (
              <li
                key={c.key}
                className="flex flex-wrap items-baseline justify-between gap-2 rounded-control border border-border-subtle px-3 py-2"
              >
                <b className="text-fg">{c.label}</b>
                <span className="text-fg-muted">
                  {c.value} to <span className="text-fg">{draft[c.key]}</span>
                </span>
              </li>
            ))}
          </ul>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={saving}
              onClick={() => setReview(false)}
            >
              Cancel
            </Button>
            <Button type="button" disabled={saving} onClick={() => void save()}>
              {saving ? "Saving" : "Save changes"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsColumn>
  )
}

function describe(f: SettingField, to: string) {
  return `${f.label}: ${f.value.replace(/ minutes$/, "")} to ${to}`
}

function Bar({
  children,
  tone,
  role,
}: {
  children: React.ReactNode
  tone?: "danger" | "ok"
  role?: "alert" | "status"
}) {
  return (
    <div
      role={role}
      className={`flex items-center gap-2.5 rounded-panel border bg-elevated px-3.5 py-2.5 text-xs shadow-lg max-sm:flex-wrap ${
        tone === "danger" ? "border-danger" : "border-surface-hover"
      } [&>span:first-child]:flex-1`}
    >
      {children}
    </div>
  )
}

function FieldRow({
  field,
  value,
  disabled,
  onChange,
}: {
  field: SettingField
  value: string
  disabled: boolean
  onChange: (v: string) => void
}) {
  const locked = !!field.readOnlyReason
  const off = locked || disabled
  const note = [
    locked
      ? field.operatorOnly
        ? "Operator access needed"
        : field.readOnlyReason?.replace(/\.$/, "")
      : "",
    field.restartRequired ? "Applies after restart" : "",
  ].filter(Boolean)
  return (
    <div className="flex flex-wrap items-center justify-between gap-4 border-t border-border-subtle px-4 py-3 first:border-t-0">
      <div className="flex max-w-80 flex-col gap-0.5">
        <p className="font-bold text-fg">{field.label}</p>
        {field.help && <p className="text-xs leading-normal text-fg-muted">{field.help}</p>}
        {note.length > 0 && (
          <p className="text-xs leading-normal text-fg-muted">
            {note.join(". ")}.
            {field.operatorOnly && (
              <>
                {" "}
                <a href="/user" className="text-fg underline">
                  Get operator access
                </a>
              </>
            )}
          </p>
        )}
      </div>
      <div className="flex items-center gap-2">
        {field.kind === "enum" && (
          <Select value={value} disabled={off} onValueChange={(v) => v && onChange(String(v))}>
            <SelectTrigger aria-label={field.label} className="h-7 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {field.options.map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        {field.kind === "boolean" && (
          <Switch
            aria-label={field.label}
            checked={value === "true"}
            disabled={off}
            onCheckedChange={(c) => onChange(String(c))}
          />
        )}
        {(field.kind === "integer" || field.kind === "string") && (
          <Input
            aria-label={field.label}
            type={field.kind === "integer" ? "number" : "text"}
            min={field.min}
            max={field.max}
            value={value}
            disabled={off}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 w-36 text-xs"
          />
        )}
        {/* Registered settings only; the manifest names the layer the value comes from. */}
        <span title={field.source.label}>
          <Pill className="rounded-xs font-mono text-caption normal-case">
            {SOURCE[field.source.kind] ?? field.source.kind}
          </Pill>
        </span>
      </div>
    </div>
  )
}
