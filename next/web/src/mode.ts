import { useSyncExternalStore } from "react"

/** The user's colour-mode choice. `system` follows prefers-color-scheme. */
export type Mode = "light" | "dark" | "system"
/** What actually paints: `system` resolved against the OS preference. */
export type ResolvedMode = "light" | "dark"

/** The one palette. Its tokens ship in design-tokens (themes.css); modes switch light/dark. */
export const NANITE_DEFAULT = "nanite-default"
export const MODE_KEY = "mode"
export const DEFAULT_MODE: Mode = "system"
export const MODES: readonly Mode[] = ["light", "dark", "system"]

const DARK_QUERY = "(prefers-color-scheme: dark)"

export function isMode(value: unknown): value is Mode {
  return typeof value === "string" && (MODES as readonly string[]).includes(value)
}

export function readMode(storage: Pick<Storage, "getItem"> | null = safeStorage()): Mode {
  try {
    const stored = storage?.getItem(MODE_KEY)
    return isMode(stored) ? stored : DEFAULT_MODE
  } catch {
    return DEFAULT_MODE
  }
}

export function resolveMode(mode: Mode, systemPrefersDark: boolean): ResolvedMode {
  if (mode === "system") return systemPrefersDark ? "dark" : "light"
  return mode
}

function safeStorage(): Storage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(DARK_QUERY).matches
}

/** Kept in step with the pre-paint script in index.html. */
export function applyMode(mode: Mode, root: HTMLElement = document.documentElement) {
  root.setAttribute("data-mode", resolveMode(mode, systemPrefersDark()))
}

const listeners = new Set<() => void>()
let current: Mode = DEFAULT_MODE
let started = false

function emit() {
  for (const l of listeners) l()
}

/** Read the stored choice, apply it, and follow OS changes while on `system`. */
export function initMode() {
  if (started) return
  started = true
  current = readMode()
  applyMode(current)
  if (typeof window.matchMedia === "function") {
    window.matchMedia(DARK_QUERY).addEventListener("change", () => {
      if (current === "system") applyMode(current)
    })
  }
  window.addEventListener("storage", (e) => {
    if (e.key !== MODE_KEY) return
    current = readMode()
    applyMode(current)
    emit()
  })
}

/** Applies immediately and persists; there is no save step. */
export function setMode(mode: Mode) {
  current = mode
  try {
    safeStorage()?.setItem(MODE_KEY, mode)
  } catch {
    // Private mode or quota: the choice still applies for this session.
  }
  applyMode(mode)
  emit()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useMode(): [Mode, (mode: Mode) => void] {
  const mode = useSyncExternalStore(
    subscribe,
    () => current,
    () => DEFAULT_MODE,
  )
  return [mode, setMode]
}
