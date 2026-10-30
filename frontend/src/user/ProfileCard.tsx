import { Button, Input } from "@hollis-labs/design-components"
import { useId, useRef, useState } from "react"
import { Chip, SetCard } from "../settings/parts"
import { initials, type Profile, saveProfile, useProfile } from "./profile"

/**
 * Profile, built locally (the kit has no account component pinned). One thin
 * component so a kit one can replace it. Stored on this device until
 * PUT /account/profile exists. `elevated` only changes the assurance chip.
 */
export function ProfileCard({ elevated = false }: { elevated?: boolean }) {
  const saved = useProfile()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Profile>(saved)
  const edit = useRef<HTMLButtonElement>(null)
  const id = useId()
  const dirty = draft.name !== saved.name || draft.email !== saved.email
  const identified = saved.name.trim() !== ""

  const close = () => {
    setEditing(false)
    // Focus returns to the opener once it is back in the DOM.
    requestAnimationFrame(() => edit.current?.focus())
  }

  const chips = (
    <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label="Identity">
      <li>
        <Chip>
          {identified && <i className="inline-block size-1.5 rounded-full bg-success" />}
          {identified ? "Identified" : "Unknown"}
        </Chip>
      </li>
      <li>
        <Chip>Assurance: {elevated ? "Operator verified" : "local"}</Chip>
      </li>
      <li>
        <Chip>Source: this app</Chip>
      </li>
    </ul>
  )

  return (
    <SetCard>
      <form
        className="flex flex-col"
        onKeyDown={(e) => {
          if (e.key === "Escape" && editing) close()
        }}
        onSubmit={(e) => {
          e.preventDefault()
          if (!dirty) return
          saveProfile(draft)
          close()
        }}
      >
        <div className="flex flex-wrap items-start gap-4 px-4 py-[18px]">
          <span
            aria-hidden="true"
            className="flex size-14 shrink-0 items-center justify-center rounded-full bg-surface font-mono text-lg font-semibold text-fg-secondary"
          >
            {initials(saved.name)}
          </span>
          <div className="flex min-w-[200px] flex-1 flex-col gap-2">
            {editing ? (
              <div className="flex flex-col gap-2.5">
                {(
                  [
                    ["name", "Display name", "text"],
                    ["email", "Email", "email"],
                  ] as const
                ).map(([k, label, type]) => (
                  <div key={k} className="flex flex-col gap-1.5">
                    <label
                      htmlFor={`${id}-${k}`}
                      className="font-mono text-caption uppercase leading-none tracking-label text-fg-muted"
                    >
                      {label}
                    </label>
                    <Input
                      id={`${id}-${k}`}
                      type={type}
                      value={draft[k]}
                      onChange={(e) => setDraft({ ...draft, [k]: e.target.value })}
                    />
                  </div>
                ))}
              </div>
            ) : (
              <div className="flex flex-col gap-0.5">
                <b className="text-lg text-fg">{saved.name || "Not set"}</b>
                <span className="text-fg-muted">{saved.email || "No email"}</span>
              </div>
            )}
            {chips}
          </div>
          {!editing && (
            <Button
              ref={edit}
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                setDraft(saved)
                setEditing(true)
              }}
            >
              Edit profile
            </Button>
          )}
        </div>
        {editing && (
          <div className="flex flex-wrap items-center gap-2 border-t border-border-subtle px-4 py-3.5">
            <span className="flex-1 text-label text-fg-muted">
              Shown on approvals and comments.
            </span>
            <Button type="button" variant="outline" size="sm" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={!dirty}>
              Save profile
            </Button>
          </div>
        )}
      </form>
    </SetCard>
  )
}
