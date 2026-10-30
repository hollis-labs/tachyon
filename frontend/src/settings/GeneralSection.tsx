import { Button } from "@hollis-labs/design-components"
import {
  type SettingsChanges,
  type SettingsDraft,
  type SettingsGroup,
  SettingsProvenanceRenderer,
  type SettingsProvenanceState,
} from "@hollis-labs/kit-settings"
import { useState } from "react"

/** What the host knows about the settings service. */
export type GeneralSettings =
  | { status: "loading" }
  | { status: "unavailable"; reason: string }
  | {
      status: "ready"
      contractVersion: number
      groups: readonly SettingsGroup[]
      states: Readonly<Record<string, SettingsProvenanceState>>
      save: (groupId: string, changes: SettingsChanges) => Promise<void>
    }

export function GeneralSection({ settings }: { settings: GeneralSettings }) {
  return (
    <section aria-labelledby="settings-general-h" className="space-y-4">
      <h2 id="settings-general-h" tabIndex={-1} className="sr-only">
        General
      </h2>
      {settings.status === "loading" && (
        <div aria-busy="true" className="space-y-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-9 animate-pulse rounded-md bg-muted" />
          ))}
        </div>
      )}
      {settings.status === "unavailable" && (
        <div className="rounded-md border border-border bg-panel px-4 py-3">
          <p className="text-sm font-medium text-foreground">Settings are not available</p>
          <p className="text-xs text-text-subtle">{settings.reason}</p>
        </div>
      )}
      {settings.status === "ready" && <Ready settings={settings} />}
    </section>
  )
}

function Ready({ settings }: { settings: Extract<GeneralSettings, { status: "ready" }> }) {
  const [drafts, setDrafts] = useState<Record<string, SettingsDraft>>({})
  const [saving, setSaving] = useState<string | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})

  const states = Object.fromEntries(
    settings.groups.map((g) => [
      g.id,
      {
        ...settings.states[g.id],
        draft: drafts[g.id] ?? {},
        busy: saving === g.id,
        error: errors[g.id],
      },
    ]),
  )

  return (
    <SettingsProvenanceRenderer
      contractVersion={settings.contractVersion}
      groups={settings.groups}
      states={states}
      onDraftChange={(id, d) => setDrafts((all) => ({ ...all, [id]: d }))}
      onSave={async (id, changes) => {
        setSaving(id)
        setErrors((e) => ({ ...e, [id]: "" }))
        try {
          await settings.save(id, changes)
          setDrafts((all) => ({ ...all, [id]: {} }))
        } catch (e) {
          // Draft is kept; the error shows under the group.
          setErrors((all) => ({ ...all, [id]: e instanceof Error ? e.message : "Save failed" }))
        } finally {
          setSaving(null)
        }
      }}
      groupFooter={(id) => {
        const n = Object.keys(drafts[id] ?? {}).length
        return n === 0 ? null : (
          <div className="flex items-center gap-2 text-xs text-text-subtle">
            <span>
              {n} {n === 1 ? "change" : "changes"}
            </span>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setDrafts((all) => ({ ...all, [id]: {} }))}
            >
              Discard
            </Button>
          </div>
        )
      }}
    />
  )
}
