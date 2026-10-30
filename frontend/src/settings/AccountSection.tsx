import { Button } from "@hollis-labs/design-components"
import { useId, useState } from "react"
import { type Profile, readProfile, writeProfile } from "./profile"

/**
 * Account, built locally: the kit has no account component yet (CW-20261030-0106).
 * Kept as one thin component so a kit one can replace it.
 */
export function AccountSection() {
  const [saved, setSaved] = useState<Profile>(() => readProfile())
  const [draft, setDraft] = useState<Profile>(saved)
  const id = useId()
  const dirty = draft.name !== saved.name || draft.email !== saved.email

  return (
    <section aria-labelledby="settings-account-h" className="space-y-4">
      <h2 id="settings-account-h" tabIndex={-1} className="sr-only">
        Account
      </h2>
      <form
        className="max-w-md space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          writeProfile(draft)
          setSaved(draft)
        }}
      >
        {(["name", "email"] as const).map((k) => (
          <label key={k} htmlFor={`${id}-${k}`} className="block space-y-1">
            <span className="text-xs capitalize text-text-subtle">{k}</span>
            <input
              id={`${id}-${k}`}
              type={k === "email" ? "email" : "text"}
              value={draft[k]}
              onChange={(e) => setDraft({ ...draft, [k]: e.target.value })}
              className="w-full rounded-md border border-border bg-panel px-2 py-1.5 text-sm text-foreground"
            />
          </label>
        ))}
        <p className="text-xs text-text-subtle">Stored on this device only.</p>
        <Button type="submit" disabled={!dirty}>
          Save profile
        </Button>
      </form>
      <dl className="flex flex-wrap gap-2 text-xs" aria-label="Identity">
        {[
          ["Identity", "Unknown"],
          ["Assurance", "local"],
          ["Source", "this app"],
        ].map(([k, v]) => (
          <div key={k} className="rounded-full border border-border px-2 py-0.5">
            <dt className="inline text-text-subtle">{k}: </dt>
            <dd className="inline text-foreground">{v}</dd>
          </div>
        ))}
      </dl>
      <Button type="button" disabled title="Available once sign-in ships">
        Set up sign-in
      </Button>
    </section>
  )
}
