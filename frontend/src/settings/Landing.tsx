import { hostFetch } from "@tachyon/host-sdk"
import { useEffect, useState } from "react"
import { Navigate } from "react-router-dom"

/** Route for each Settings › General › Landing page value (internal/server LandingPages). */
export const LANDING_ROUTES: Record<string, string> = {
  planner: "/planner",
  chat: "/chat",
  agents: "/agents",
  approvals: "/approvals",
  transcripts: "/dashboards/transcripts",
  repos: "/repos",
  builder: "/builder",
}

const CACHE = "tachyon.landing"
const SETTING = "/api/plugins/console/v1/admin/settings/general"

export function landingRoute(value: unknown, fallback: string): string {
  return (typeof value === "string" && LANDING_ROUTES[value]) || fallback
}

function cached(): string | null {
  try {
    return localStorage.getItem(CACHE)
  } catch {
    return null
  }
}

/** Called after Settings saves a new landing page, so the next '/' opens it without waiting. */
export function rememberLanding(value: string, fallback: string) {
  try {
    localStorage.setItem(CACHE, landingRoute(value, fallback))
  } catch {}
}

/** Reads the landing page setting; the last value read opens at once and the fetch refreshes it for next time. */
export async function readLanding(fallback: string): Promise<string> {
  const res = await hostFetch(SETTING, { headers: { Accept: "application/json" } })
  if (!res.ok) throw new Error(`settings ${res.status}`)
  const snap = (await res.json()) as { values?: Record<string, { value?: unknown }> }
  const to = landingRoute(snap.values?.landing_page?.value, fallback)
  try {
    localStorage.setItem(CACHE, to)
  } catch {}
  return to
}

/** '/' opens the section chosen in Settings › General › Landing page. */
export function Landing({ fallback }: { fallback: string }) {
  const [to, setTo] = useState<string | null>(cached)
  useEffect(() => {
    let live = true
    readLanding(fallback).then(
      (route) => live && setTo((cur) => cur ?? route),
      () => live && setTo((cur) => cur ?? fallback),
    )
    return () => {
      live = false
    }
  }, [fallback])
  return to ? <Navigate to={to} replace /> : null
}
