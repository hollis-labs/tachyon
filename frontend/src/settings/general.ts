/** General settings model. Shaped like the admin manifest so the real service can replace the fixture. */
export type Source = "default" | "overridden" | "env"

export interface SettingField {
  key: string
  label: string
  help: string
  options: readonly { value: string; label: string }[]
  value: string
  source: Source
  /** Set when the operator, not this user, owns the value. */
  readOnlyReason?: string
  /** Shown as a note under the row. */
  note?: string
}

export interface SettingGroup {
  id: string
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
  save: (groupId: string, changes: Readonly<Record<string, string>>) => Promise<SaveResult>
}

const opts = (...labels: string[]) => labels.map((l) => ({ value: l, label: l }))

export const FIXTURE: readonly SettingGroup[] = [
  {
    id: "behaviour",
    fields: [
      {
        key: "timezone",
        label: "Timezone",
        help: "Used for quiet hours and daily summaries.",
        options: opts(
          "America/Chicago",
          "America/New_York",
          "America/Los_Angeles",
          "Europe/London",
          "UTC",
        ),
        value: "America/Chicago",
        source: "default",
      },
      {
        key: "stale_threshold",
        label: "Stale agent threshold",
        help: "Working or waiting agents with no events for this long are marked stale.",
        options: opts("5 minutes", "15 minutes", "30 minutes", "60 minutes"),
        value: "15 minutes",
        source: "overridden",
      },
      {
        key: "landing",
        label: "Landing page",
        help: "Where Tachyon opens.",
        options: opts("Planner", "Chat", "Agents", "Approvals"),
        value: "Planner",
        source: "default",
      },
    ],
  },
  {
    id: "access",
    fields: [
      {
        key: "elevation_ttl",
        label: "Elevated access lasts",
        help: "How long elevated access stays on before it drops.",
        options: opts("15 minutes", "30 minutes", "60 minutes"),
        value: "30 minutes",
        source: "default",
        readOnlyReason: "Operator access needed",
        note: "Applies after restart",
      },
    ],
  },
]

/**
 * Stand-in until the settings manifest route is live. `?settings-stub=fail|conflict|slow|error`
 * drives the failure states for review.
 */
export function stubApi(mode: string | null = null): GeneralApi {
  let groups = structuredClone(FIXTURE) as SettingGroup[]
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))
  return {
    async load() {
      await wait(mode === "slow" ? 2500 : 150)
      if (mode === "error") throw new Error("The settings service did not answer.")
      return structuredClone(groups)
    },
    async save(groupId, changes) {
      await wait(250)
      if (mode === "conflict") {
        groups = groups.map((g) =>
          g.id === groupId
            ? {
                ...g,
                fields: g.fields.map((f) => ({
                  ...f,
                  value: f.options[f.options.length - 1].value,
                  source: "overridden" as Source,
                })),
              }
            : g,
        )
        throw new ConflictError("This changed elsewhere")
      }
      const results: Record<string, FieldResult> = {}
      const keys = Object.keys(changes)
      keys.forEach((k, i) => {
        if (mode === "fail" && i === keys.length - 1 && keys.length > 0) {
          results[k] = { error: "The service refused this value." }
        } else {
          results[k] = "ok"
        }
      })
      groups = groups.map((g) =>
        g.id === groupId
          ? {
              ...g,
              fields: g.fields.map((f) =>
                results[f.key] === "ok" && f.key in changes
                  ? { ...f, value: changes[f.key], source: "overridden" as Source }
                  : f,
              ),
            }
          : g,
      )
      return { results }
    },
  }
}
