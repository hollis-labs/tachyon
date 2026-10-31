import { hostFetch } from "@tachyon/host-sdk"

/** One setting as the admin manifest and its snapshot describe it. Values are held as strings for editing. */
export type FieldKind = "enum" | "integer" | "string" | "boolean"

export interface SettingField {
  key: string
  label: string
  help: string
  kind: FieldKind
  options: readonly string[]
  min?: number
  max?: number
  value: string
  /** Where the value comes from: default, file, env, override. */
  source: { kind: string; label: string }
  /** Set when the value cannot be changed here. */
  readOnlyReason?: string
  /** True when the reason is that the operator, not this user, owns the value. */
  operatorOnly?: boolean
  restartRequired?: boolean
}

export interface SettingGroup {
  id: string
  label: string
  revision: string
  etag: string
  fields: readonly SettingField[]
}

export type FieldResult = "ok" | { error: string }

export interface SaveResult {
  results: Readonly<Record<string, FieldResult>>
}

/** 409, 412 and 428: the value moved under the user. */
export class ConflictError extends Error {}

export interface GeneralApi {
  load: () => Promise<readonly SettingGroup[]>
  save: (group: SettingGroup, changes: Readonly<Record<string, string>>) => Promise<SaveResult>
}

const ROOT = "/api/plugins/console/v1/admin"
// Alerts has its own section and Agents stays 'soon'.
const HIDDEN = new Set(["alerts", "agents"])

interface Wire {
  contract_version?: number
  revision: string
  settings: {
    id: string
    label: string
    schema: {
      properties?: Record<
        string,
        {
          type: string
          title?: string
          description?: string
          enum?: unknown[]
          minimum?: number
          maximum?: number
        }
      >
    }
    fields: Record<
      string,
      { editable: boolean; restart_required?: boolean; read_only_reason?: string }
    >
    capabilities?: { can_update?: boolean }
  }[]
}
interface Snapshot {
  values: Record<
    string,
    {
      value?: unknown
      source?: { kind: string; label: string }
      editable: boolean
      read_only_reason?: string
    }
  >
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw await failure(res)
  return (await res.json()) as T
}

async function failure(res: Response): Promise<Error> {
  let code = ""
  let message = `Request failed (${res.status})`
  let errors: { path?: string; message?: string }[] = []
  try {
    const body = (await res.json()) as {
      error?: { code?: string; message?: string; errors?: typeof errors }
      code?: string
      message?: string
      errors?: typeof errors
    }
    code = body.error?.code ?? body.code ?? ""
    message = body.error?.message ?? body.message ?? message
    errors = body.error?.errors ?? body.errors ?? []
  } catch {
    // Non-JSON body: keep the status text.
  }
  if (res.status === 409 || res.status === 412 || res.status === 428)
    return new ConflictError(message)
  const e = new Error(
    res.status === 401 || res.status === 403
      ? "Operator access is needed to change settings."
      : message,
  ) as Error & { errors?: typeof errors; code?: string }
  e.errors = errors
  e.code = code
  return e
}

const OPERATOR = /operator/i

export function realApi(): GeneralApi {
  return {
    async load() {
      const manifest = await json<Wire>(
        await hostFetch(`${ROOT}/manifest`, { headers: { Accept: "application/json" } }),
      )
      const groups = manifest.settings.filter((g) => !HIDDEN.has(g.id))
      return Promise.all(
        groups.map(async (g): Promise<SettingGroup> => {
          const res = await hostFetch(`${ROOT}/settings/${g.id}`, {
            headers: { Accept: "application/json" },
          })
          const snap = await json<Snapshot>(res)
          const fields = Object.entries(g.schema.properties ?? {}).map(([key, p]): SettingField => {
            const meta = g.fields[key]
            const v = snap.values[key]
            const reason =
              v?.read_only_reason ??
              meta?.read_only_reason ??
              (v && !v.editable ? "Read only." : undefined)
            const kind: FieldKind = p.enum
              ? "enum"
              : p.type === "integer"
                ? "integer"
                : p.type === "boolean"
                  ? "boolean"
                  : "string"
            return {
              key,
              label: p.title ?? key,
              help: p.description ?? "",
              kind,
              options: (p.enum ?? []).map(String),
              min: p.minimum,
              max: p.maximum,
              value: v?.value === undefined || v.value === null ? "" : String(v.value),
              source: v?.source ?? { kind: "default", label: "Built-in default" },
              readOnlyReason: reason,
              operatorOnly: !!reason && OPERATOR.test(reason),
              restartRequired: meta?.restart_required,
            }
          })
          return {
            id: g.id,
            label: g.label,
            revision: manifest.revision,
            etag: res.headers.get("ETag") ?? "",
            fields,
          }
        }),
      )
    },
    async save(group, changes) {
      const kinds = Object.fromEntries(group.fields.map((f) => [f.key, f.kind]))
      const set = Object.fromEntries(
        Object.entries(changes).map(([k, v]) => [
          k,
          kinds[k] === "integer" ? Number(v) : kinds[k] === "boolean" ? v === "true" : v,
        ]),
      )
      try {
        await json(
          await hostFetch(`${ROOT}/settings/${group.id}/update`, {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              Accept: "application/json",
              "If-Match": group.etag,
            },
            body: JSON.stringify({ revision: group.revision, set, unset: [] }),
          }),
        )
        return { results: Object.fromEntries(Object.keys(changes).map((k) => [k, "ok" as const])) }
      } catch (e) {
        if (e instanceof ConflictError) throw e
        const errs = (e as { errors?: { path?: string; message?: string }[] }).errors ?? []
        const msg = e instanceof Error ? e.message : "Save failed"
        // A validation failure names the keys it refused; the rest are refused with them (one atomic update per group).
        return {
          results: Object.fromEntries(
            Object.keys(changes).map((k) => [
              k,
              {
                error:
                  errs.find((x) => x.path?.replace(/^\//, "").replace(/^\$\./, "") === k)
                    ?.message ?? msg,
              },
            ]),
          ),
        }
      }
    },
  }
}

/** Dev only: `?settings-stub=fail|conflict|slow|error` shows the failure states without a backend. */
export function devStubApi(mode: string): GeneralApi {
  const field = (
    key: string,
    label: string,
    help: string,
    options: string[],
    value: string,
    source = "default",
    extra: Partial<SettingField> = {},
  ): SettingField => ({
    key,
    label,
    help,
    kind: "enum",
    options,
    value,
    source: { kind: source, label: source },
    ...extra,
  })
  let fields: SettingField[] = [
    field(
      "timezone",
      "Timezone",
      "Used for quiet hours and daily summaries.",
      ["America/Chicago", "UTC"],
      "America/Chicago",
    ),
    field(
      "stale",
      "Stale agent threshold",
      "Working or waiting agents with no events for this long are marked stale.",
      ["15 minutes", "30 minutes"],
      "15 minutes",
      "override",
    ),
    field(
      "ttl",
      "Elevated access lasts",
      "How long elevated access stays on.",
      ["30 minutes"],
      "30 minutes",
      "default",
      { readOnlyReason: "Operator access needed", operatorOnly: true, restartRequired: true },
    ),
  ]
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))
  const group = (): SettingGroup => ({
    id: "general",
    label: "General",
    revision: "dev",
    etag: "g0",
    fields,
  })
  return {
    async load() {
      await wait(mode === "slow" ? 2500 : 150)
      if (mode === "error") throw new Error("The settings service did not answer.")
      return [group()]
    },
    async save(_g, changes) {
      await wait(250)
      if (mode === "conflict") throw new ConflictError("This changed elsewhere")
      const keys = Object.keys(changes)
      const results: Record<string, FieldResult> = {}
      for (const [i, k] of keys.entries())
        results[k] =
          mode === "fail" && i === keys.length - 1
            ? { error: "The service refused this value." }
            : "ok"
      fields = fields.map((f) =>
        results[f.key] === "ok"
          ? { ...f, value: changes[f.key], source: { kind: "override", label: "override" } }
          : f,
      )
      return { results }
    },
  }
}
