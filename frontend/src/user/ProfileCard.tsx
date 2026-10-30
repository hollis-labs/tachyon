import { Button } from "@hollis-labs/design-components"
import { useId, useRef, useState } from "react"
import { Card } from "../settings/Card"
import { initials, type Profile, saveProfile, useProfile } from "./profile"

const CHIP = "rounded-full border border-border px-2 py-0.5 text-xs"

/**
 * Profile, built locally (the kit has no account component pinned). One thin
 * component so a kit one can replace it. Stored on this device until
 * PUT /account/profile exists.
 */
export function ProfileCard() {
  const saved = useProfile()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Profile>(saved)
  const edit = useRef<HTMLButtonElement>(null)
  const id = useId()
  const dirty = draft.name !== saved.name || draft.email !== saved.email

  const close = () => {
    setEditing(false)
    // Focus returns to the opener once it is back in the DOM.
    requestAnimationFrame(() => edit.current?.focus())
  }

  return (
    <Card title="Profile">
      <div className="flex items-start gap-3">
        <div
          aria-hidden="true"
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-border text-sm font-medium text-foreground"
        >
          {initials(saved.name)}
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          {editing ? (
            <form
              className="space-y-3"
              onKeyDown={(e) => {
                if (e.key === "Escape") close()
              }}
              onSubmit={(e) => {
                e.preventDefault()
                saveProfile(draft)
                close()
              }}
            >
              {(
                [
                  ["name", "Display name", "text"],
                  ["email", "Email", "email"],
                ] as const
              ).map(([k, label, type]) => (
                <label key={k} htmlFor={`${id}-${k}`} className="block space-y-1">
                  <span className="text-xs text-text-subtle">{label}</span>
                  <input
                    id={`${id}-${k}`}
                    type={type}
                    value={draft[k]}
                    onChange={(e) => setDraft({ ...draft, [k]: e.target.value })}
                    className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-foreground"
                  />
                </label>
              ))}
              <div className="flex items-center justify-between gap-2 border-t border-border-subtle pt-3">
                <p className="text-xs text-text-subtle">
                  Shown on approvals, comments and the audit trail.
                </p>
                <div className="flex shrink-0 gap-2">
                  <Button type="button" variant="ghost" onClick={close}>
                    Cancel
                  </Button>
                  <Button type="submit" disabled={!dirty}>
                    Save profile
                  </Button>
                </div>
              </div>
            </form>
          ) : (
            <>
              <div>
                <p className="text-sm font-medium text-foreground">{saved.name || "Not set"}</p>
                <p className="text-xs text-text-subtle">{saved.email || "No email"}</p>
              </div>
              <Button
                ref={edit}
                type="button"
                variant="ghost"
                onClick={() => {
                  setDraft(saved)
                  setEditing(true)
                }}
              >
                Edit profile
              </Button>
            </>
          )}
          <ul className="flex flex-wrap gap-2" aria-label="Identity">
            <li className={CHIP}>Identity: Unknown</li>
            <li className={CHIP}>Assurance: local</li>
            <li className={CHIP}>Source: this app</li>
          </ul>
        </div>
      </div>
    </Card>
  )
}
