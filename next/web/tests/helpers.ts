import { mkdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import type { Page } from "@playwright/test"
import { authState, ownerName, passwordFile, shots } from "../e2e/env"

export const ownerPassword = () => readFileSync(passwordFile, "utf8")
export const signedOut = { cookies: [], origins: [] }
export const owner = authState
export { ownerName }

/** The area surfaces, in nav order: label, route, a deep link the area owns, and whether it is still a stub. */
export const AREAS = [
  { label: "Planner", route: "/planner", deep: "/planner/streams", stub: false },
  { label: "Agent Ops", route: "/agents", deep: "/agents/sess_work", stub: false },
  { label: "Builder", route: "/builder", deep: "/builder/library/agent/a1", stub: true },
  { label: "Registry", route: "/registry", deep: "/registry/projects/p1", stub: true },
  { label: "Chat", route: "/chat", deep: "/chat/thread-9", stub: false },
  { label: "Approvals", route: "/approvals", deep: "/approvals", stub: false },
  { label: "Needs you", route: "/needs-you", deep: "/needs-you", stub: false },
  {
    label: "Transcripts",
    route: "/dashboards/transcripts",
    deep: "/dashboards/transcripts",
    stub: true,
  },
  { label: "Repos", route: "/repos", deep: "/pr/github.com/hollis-labs/tachyon/693", stub: true },
] as const

export const signInHeading = (page: Page) => page.getByRole("heading", { name: "Sign in" })

export async function signIn(page: Page, username: string, password: string) {
  await page.goto("/planner")
  await page.getByLabel("Username").fill(username)
  await page.getByLabel("Password").fill(password)
  await page.getByRole("button", { name: "Sign in" }).click()
}

/** Saves a full-page screenshot for the PR body. */
export async function shot(page: Page, name: string) {
  mkdirSync(shots, { recursive: true })
  await page.screenshot({ path: join(shots, `${name}.png`), fullPage: true })
}
