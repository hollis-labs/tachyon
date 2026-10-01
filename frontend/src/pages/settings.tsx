import { Button, EmptyState, Input, Label, PageHeader, Pill, Skeleton } from "@hollis-labs/sysop-ui"
import { RefreshCw, RotateCcw, Save, Settings, Zap } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import {
  type ConfigTarget,
  envelopeError,
  getConfig,
  listConfigTargets,
  PluginRestartError,
  resetConfig,
  restartPlugin,
  type SettingsField,
  setConfig,
  type TargetConfig,
  validationErrors,
} from "../api/settings"
import { useVerbs } from "../api/verbs"

function message(error: unknown): string {
  return error instanceof Error ? error.message : "Could not complete the request."
}

function drafts(config: TargetConfig): Record<string, string | boolean> {
  return Object.fromEntries(
    (config.target.settings.fields ?? []).map((field) => {
      const value = config.values[field.key]
      return [field.key, field.type === "boolean" ? value === true : String(value ?? "")]
    }),
  )
}

export function SettingsPage() {
  const verbs = useVerbs()
  const [targets, setTargets] = useState<ConfigTarget[]>([])
  const [selected, setSelected] = useState<string>("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [operationError, setOperationError] = useState<string | null>(null)
  const [refresh, setRefresh] = useState(0)
  const [restartNeeded, setRestartNeeded] = useState<Record<string, boolean>>({})

  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh explicitly reloads the server snapshot.
  useEffect(() => {
    if (verbs.loading) return
    if (!verbs.has("config_list")) {
      setLoading(false)
      setTargets([])
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    listConfigTargets()
      .then((result) => {
        if (!active) return
        if (result.status !== "ok") throw new Error(envelopeError(result))
        setTargets(result.data)
        setSelected((current) => {
          if (result.data.some((target) => target.id === current)) return current
          return (
            result.data.find((target) => (target.settings.fields?.length ?? 0) > 0)?.id ??
            result.data[0]?.id ??
            ""
          )
        })
      })
      .catch((failure: unknown) => {
        if (active) setError(message(failure))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [verbs, refresh])

  const markRestart = useCallback((plugin: string, needed: boolean) => {
    setRestartNeeded((current) => ({ ...current, [plugin]: needed }))
  }, [])
  const target = targets.find((item) => item.id === selected)

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Settings / Plugins" />
      <div className="flex items-center justify-between gap-4 border-b border-border px-5 py-3">
        <p className="text-sm text-text-soft">
          Configure loaded plugins. Saved changes apply when a plugin restarts.
        </p>
        <Button
          variant="outline"
          size="sm"
          disabled={busy || loading || verbs.loading}
          onClick={() => {
            setOperationError(null)
            setRefresh((current) => current + 1)
          }}
        >
          <RefreshCw className="h-3.5 w-3.5" />
          Refresh
        </Button>
      </div>
      {(error || operationError) && (
        <div role="alert" className="border-b border-border bg-status-failed/10 px-5 py-3 text-sm">
          {operationError || error}
        </div>
      )}
      {verbs.loading || (loading && targets.length === 0) ? (
        <div className="space-y-3 p-5">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      ) : !verbs.has("config_list") ? (
        <EmptyState
          variant="empty"
          title="Configuration is unavailable"
          description="The config module is not loaded."
        />
      ) : targets.length === 0 ? (
        <EmptyState variant="empty" title="No loaded plugins" description="Refresh to try again." />
      ) : (
        <div className="grid min-h-0 flex-1 gap-5 overflow-auto p-5 lg:grid-cols-[260px_minmax(0,1fr)]">
          <aside aria-label="Loaded plugins" className="space-y-1">
            <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-text-subtle">
              Loaded plugins · {targets.length}
            </h2>
            {targets.map((item) => (
              <button
                key={item.id}
                type="button"
                disabled={busy}
                aria-pressed={selected === item.id}
                onClick={() => setSelected(item.id)}
                className={`flex w-full items-start justify-between gap-2 rounded-md border p-3 text-left transition-colors disabled:opacity-60 ${
                  selected === item.id
                    ? "border-border bg-panel-2"
                    : "border-transparent hover:bg-panel-1"
                }`}
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">{item.name || item.id}</span>
                  <span className="block truncate font-mono text-xs text-text-subtle">
                    {item.id}
                  </span>
                  <span className="mt-1 block text-xs text-text-soft">
                    {item.settings.fields?.length ?? 0} settings
                  </span>
                </span>
                {restartNeeded[item.id] && <Zap aria-label="Restart needed" className="h-4 w-4" />}
              </button>
            ))}
          </aside>
          {target && (
            <SettingsEditor
              key={target.id}
              target={target}
              refresh={refresh}
              canRead={verbs.has("config_get")}
              canSave={verbs.has("config_set")}
              canReset={verbs.has("config_reset")}
              restartNeeded={restartNeeded[target.id] === true}
              onRestartNeeded={markRestart}
              onBusy={setBusy}
              onRestart={() => setRefresh((current) => current + 1)}
              onRestartFailure={(detail) => {
                setOperationError(detail)
                setTargets((current) => current.filter((item) => item.id !== target.id))
                setSelected(targets.find((item) => item.id !== target.id)?.id ?? "")
                setRefresh((current) => current + 1)
              }}
            />
          )}
        </div>
      )}
    </div>
  )
}

interface EditorProps {
  target: ConfigTarget
  refresh: number
  canRead: boolean
  canSave: boolean
  canReset: boolean
  restartNeeded: boolean
  onRestartNeeded(plugin: string, needed: boolean): void
  onBusy(busy: boolean): void
  onRestart(): void
  onRestartFailure(detail: string): void
}

function SettingsEditor({
  target,
  refresh,
  canRead,
  canSave,
  canReset,
  restartNeeded,
  onRestartNeeded,
  onBusy,
  onRestart,
  onRestartFailure,
}: EditorProps) {
  const [config, setCurrent] = useState<TargetConfig | null>(null)
  const [draft, setDraft] = useState<Record<string, string | boolean>>({})
  const [dirty, setDirty] = useState<Record<string, boolean>>({})
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [action, setAction] = useState<"save" | "reset" | "restart" | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  const active = useRef(true)

  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])

  const apply = useCallback((value: TargetConfig) => {
    setCurrent(value)
    setDraft(drafts(value))
    setDirty({})
    setFieldErrors(value.validation.errors ?? {})
  }, [])

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload retries a failed read; refresh reloads the host snapshot.
  useEffect(() => {
    if (!canRead) {
      setLoading(false)
      return
    }
    let current = true
    setLoading(true)
    setError(null)
    getConfig(target.id)
      .then((result) => {
        if (!current) return
        if (result.status !== "ok") throw new Error(envelopeError(result))
        apply(result.data)
      })
      .catch((failure: unknown) => {
        if (current) setError(message(failure))
      })
      .finally(() => {
        if (current) setLoading(false)
      })
    return () => {
      current = false
    }
  }, [target.id, canRead, reload, refresh, apply])

  function edit(key: string, value: string | boolean) {
    setDraft((current) => ({ ...current, [key]: value }))
    setDirty((current) => ({ ...current, [key]: true }))
    setFieldErrors((current) => {
      const next = { ...current }
      delete next[key]
      return next
    })
    setNotice(null)
  }

  async function save() {
    if (!config) return
    const values: Record<string, string | boolean | number> = {}
    const errors: Record<string, string> = {}
    for (const field of config.target.settings.fields ?? []) {
      if (!dirty[field.key]) continue
      const value = draft[field.key]
      if (field.type === "number") {
        if (typeof value !== "string" || value.trim() === "" || !Number.isFinite(Number(value))) {
          errors[field.key] = "Enter a number."
        } else values[field.key] = Number(value)
      } else if (field.type === "boolean") {
        values[field.key] = value === true
      } else if (typeof value === "string") {
        if (field.required && value.trim() === "") errors[field.key] = "This value is required."
        else values[field.key] = value
      }
    }
    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return
    await run("save", async () => {
      const result = await setConfig(target.id, values)
      if (!active.current) return
      if (result.status !== "ok") {
        setFieldErrors(validationErrors(result))
        throw new Error(envelopeError(result))
      }
      apply(result.data.config)
      onRestartNeeded(target.id, result.data.restart_required)
      setNotice("Settings saved.")
    })
  }

  async function run(kind: "save" | "reset" | "restart", work: () => Promise<void>) {
    setAction(kind)
    onBusy(true)
    setError(null)
    setNotice(null)
    try {
      await work()
    } catch (failure) {
      if (active.current) setError(message(failure))
    } finally {
      if (active.current) setAction(null)
      onBusy(false)
    }
  }

  async function reset() {
    await run("reset", async () => {
      const result = await resetConfig(target.id)
      if (!active.current) return
      if (result.status !== "ok") throw new Error(envelopeError(result))
      apply(result.data.config)
      onRestartNeeded(target.id, result.data.restart_required)
      setNotice("Defaults restored.")
    })
  }

  async function restart() {
    await run("restart", async () => {
      try {
        await restartPlugin(target.id)
      } catch (failure) {
        if (failure instanceof PluginRestartError && failure.unloaded) {
          onRestartFailure(`${target.name || target.id} is unloaded: ${message(failure)}`)
        }
        throw failure
      }
      if (!active.current) return
      onRestartNeeded(target.id, false)
      setNotice("Plugin restarted. Saved settings are now active.")
      onRestart()
    })
  }

  const fields = config?.target.settings.fields ?? []
  return (
    <section className="min-w-0 rounded-lg border border-border bg-panel-1">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border p-4">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold">
            <Settings className="h-4 w-4" />
            {target.name || target.id}
          </h2>
          <p className="mt-1 font-mono text-xs text-text-subtle">{target.id}</p>
        </div>
        <Pill>Loaded</Pill>
      </div>
      {restartNeeded && (
        <div
          role="status"
          className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-panel-2 p-4"
        >
          <p className="text-sm">Restart this plugin to apply saved settings.</p>
          <Button
            size="sm"
            disabled={action !== null || Object.keys(dirty).length > 0}
            onClick={restart}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {action === "restart" ? "Restarting…" : "Restart plugin"}
          </Button>
        </div>
      )}
      <div className="space-y-4 p-4">
        {error && (
          <p role="alert" className="text-sm text-status-failed">
            {error}
          </p>
        )}
        {notice && (
          <p role="status" className="text-sm text-status-done">
            {notice}
          </p>
        )}
        {loading ? (
          <Skeleton className="h-40 w-full" />
        ) : !canRead ? (
          <p className="text-sm text-text-soft">
            This plugin does not support configuration reads.
          </p>
        ) : !config ? (
          <Button variant="outline" size="sm" onClick={() => setReload((current) => current + 1)}>
            Try again
          </Button>
        ) : fields.length === 0 ? (
          <p className="text-sm text-text-soft">This plugin declares no configurable settings.</p>
        ) : (
          <form
            onSubmit={(event) => {
              event.preventDefault()
              void save()
            }}
            className="space-y-5"
          >
            {fields.map((field) => (
              <SettingInput
                key={field.key}
                field={field}
                value={draft[field.key] ?? ""}
                disabled={action !== null || !canSave}
                error={fieldErrors[field.key]}
                onChange={(value) => edit(field.key, value)}
              />
            ))}
            {Object.entries(fieldErrors)
              .filter(([key]) => !fields.some((field) => field.key === key))
              .map(([key, detail]) => (
                <p key={key} role="alert" className="text-sm text-status-failed">
                  {detail}
                </p>
              ))}
            <div className="flex flex-wrap gap-2 border-t border-border pt-4">
              <Button
                type="submit"
                size="sm"
                disabled={action !== null || !canSave || Object.keys(dirty).length === 0}
              >
                <Save className="h-3.5 w-3.5" />
                {action === "save" ? "Saving…" : "Save settings"}
              </Button>
              {canReset && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={action !== null}
                  onClick={reset}
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                  {action === "reset" ? "Resetting…" : "Reset defaults"}
                </Button>
              )}
            </div>
          </form>
        )}
      </div>
    </section>
  )
}

function SettingInput({
  field,
  value,
  disabled,
  error,
  onChange,
}: {
  field: SettingsField
  value: string | boolean
  disabled: boolean
  error?: string
  onChange(value: string | boolean): void
}) {
  const id = `setting-${field.key}`
  const descriptionId = `${id}-description`
  const errorId = `${id}-error`
  const describedBy =
    [field.description ? descriptionId : "", error ? errorId : ""].filter(Boolean).join(" ") ||
    undefined
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>
        {field.label || field.key}
        {field.required ? " *" : ""}
      </Label>
      {field.type === "boolean" ? (
        <input
          id={id}
          type="checkbox"
          checked={value === true}
          disabled={disabled}
          aria-invalid={Boolean(error)}
          aria-describedby={describedBy}
          onChange={(event) => onChange(event.target.checked)}
          className="block h-4 w-4 accent-text"
        />
      ) : field.type === "select" ? (
        <select
          id={id}
          value={String(value)}
          disabled={disabled}
          required={field.required}
          aria-invalid={Boolean(error)}
          aria-describedby={describedBy}
          onChange={(event) => onChange(event.target.value)}
          className="h-9 w-full rounded-md border border-border bg-panel-2 px-3 text-sm text-text disabled:opacity-60"
        >
          <option value="" disabled>
            Choose a value
          </option>
          {field.options?.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label || option.value}
            </option>
          ))}
        </select>
      ) : (
        <Input
          id={id}
          type={field.type === "number" ? "number" : "text"}
          step={field.type === "number" ? "any" : undefined}
          value={String(value)}
          disabled={disabled}
          required={field.required}
          aria-invalid={Boolean(error)}
          aria-describedby={describedBy}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {field.description && (
        <p id={descriptionId} className="text-xs text-text-soft">
          {field.description}
        </p>
      )}
      {error && (
        <p id={errorId} role="alert" className="text-xs text-status-failed">
          {error}
        </p>
      )}
    </div>
  )
}
