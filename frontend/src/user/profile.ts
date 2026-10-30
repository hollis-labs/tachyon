/** Local-first profile. The server does not own this yet; it never leaves the browser. */
export interface Profile {
  name: string
  email: string
}

export const PROFILE_KEY = "profile"
const EMPTY: Profile = { name: "", email: "" }

export function readProfile(storage: Pick<Storage, "getItem"> | null = safeStorage()): Profile {
  try {
    const raw = storage?.getItem(PROFILE_KEY)
    if (!raw) return EMPTY
    const v = JSON.parse(raw) as Partial<Profile>
    return {
      name: typeof v.name === "string" ? v.name : "",
      email: typeof v.email === "string" ? v.email : "",
    }
  } catch {
    return EMPTY
  }
}

export function writeProfile(p: Profile, storage: Pick<Storage, "setItem"> | null = safeStorage()) {
  try {
    storage?.setItem(PROFILE_KEY, JSON.stringify(p))
  } catch {
    // Storage blocked: the form keeps the value for this session only.
  }
}

function safeStorage(): Storage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

// A tiny store so the avatar menu and the profile card stay in step.
import { useSyncExternalStore } from "react"

const listeners = new Set<() => void>()
let snapshot: Profile | null = null

function current(): Profile {
  if (snapshot === null) snapshot = readProfile()
  return snapshot
}

export function saveProfile(p: Profile) {
  writeProfile(p)
  snapshot = p
  for (const l of listeners) l()
}

export function useProfile(): Profile {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => {
        listeners.delete(l)
      }
    },
    current,
    () => ({ name: "", email: "" }),
  )
}

/** Up to two initials; "?" when there is no name. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return "?"
  return (parts[0][0] + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase()
}
