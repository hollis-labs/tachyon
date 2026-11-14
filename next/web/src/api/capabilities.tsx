import { createContext, type ReactNode, useContext, useEffect, useState } from "react"
import { request } from "./request"

/** The slice of GET /api/identity/profile the UI reads. */
export interface Profile {
  username?: string
  display_name?: string
  scopes: readonly string[]
}

export const OPERATOR_SCOPE = "tachyon.operator"

/**
 * Whether `scopes` grant `scope`, with the backend's rules (libs auth `Principal.HasScope`): exact,
 * `*`, or `<prefix>.*` for everything under `<prefix>.`.
 * The backend authorizes every request: this only decides what the UI offers.
 */
export function hasScope(scopes: readonly string[] | null | undefined, scope: string): boolean {
  if (!scopes) return false
  return scopes.some(
    (s) => s === "*" || s === scope || (s.endsWith(".*") && scope.startsWith(s.slice(0, -1))),
  )
}

export const fetchProfile = () => request<Profile>("/api/identity/profile")

const ScopesContext = createContext<readonly string[] | null>(null)

/**
 * The signed-in person's scopes, read from the profile (kit-identity knows them only right after
 * sign-in, not after a reload). Read again whenever the tab becomes visible, so a session that ended
 * while the tab was away returns to sign-in (the 401 goes through `request`). null until known; on
 * an error the UI offers nothing extra.
 */
export function CapabilitiesProvider({ children }: { children: ReactNode }) {
  const [scopes, setScopes] = useState<readonly string[] | null>(null)
  useEffect(() => {
    let live = true
    const load = () =>
      fetchProfile()
        .then((p) => live && setScopes(Array.isArray(p?.scopes) ? p.scopes : []))
        .catch(() => live && setScopes([]))
    const onVisible = () => {
      if (document.visibilityState === "visible") void load()
    }
    void load()
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      live = false
      document.removeEventListener("visibilitychange", onVisible)
    }
  }, [])
  return <ScopesContext.Provider value={scopes}>{children}</ScopesContext.Provider>
}

/** The caller's scopes (null until the profile is read). */
export const useScopes = () => useContext(ScopesContext)

/** Whether the caller holds `scope` (false until the profile is read). */
export const useCan = (scope: string) => hasScope(useScopes(), scope)
