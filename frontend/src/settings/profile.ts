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
    return { name: typeof v.name === "string" ? v.name : "", email: typeof v.email === "string" ? v.email : "" }
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
