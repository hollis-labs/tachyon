import { AlertsSettingsSection } from "@tachyon/console-ui"
import { useEffect, useRef } from "react"
import { NavLink, useNavigate, useParams } from "react-router-dom"
import { AppearanceSection } from "./AppearanceSection"
import { Card } from "./Card"
import { GeneralSection, type GeneralSettings } from "./GeneralSection"
import { resolveSection, SETTINGS_SECTIONS } from "./sections"

// No settings-manifest endpoint exists yet (BACKEND NEEDS in the flow spec).
const GENERAL: GeneralSettings = {
  status: "unavailable",
  reason: "The settings service is not connected yet. Nothing here has been changed.",
}

export function SettingsPage() {
  const { section } = useParams()
  const navigate = useNavigate()
  const active = resolveSection(section)
  const panel = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (section !== active) navigate(`/settings/${active}`, { replace: true })
  }, [section, active, navigate])

  // Land with focus on the section heading.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-run when the section changes
  useEffect(() => {
    panel.current?.querySelector<HTMLElement>("h2")?.focus()
  }, [active])

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-4">
        <span className="font-mono text-xs uppercase tracking-widest text-text-subtle">
          Settings
        </span>
        <span className="text-xs text-text-subtle">Saved on this device</span>
      </header>
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <nav
          aria-label="Settings sections"
          className="flex shrink-0 gap-1 overflow-x-auto border-b border-border p-2 md:w-52 md:flex-col md:border-b-0 md:border-r"
        >
          {SETTINGS_SECTIONS.map((s) =>
            s.soon ? (
              <span
                key={s.id}
                aria-disabled="true"
                className="rounded-md px-3 py-1.5 text-sm text-text-subtle opacity-60"
              >
                {s.label} <span className="text-xs">soon</span>
              </span>
            ) : (
              <NavLink
                key={s.id}
                to={`/settings/${s.id}`}
                aria-current={active === s.id ? "page" : undefined}
                className={`rounded-md px-3 py-1.5 text-sm transition-colors hover:bg-panel-hover ${
                  active === s.id ? "bg-panel-hover text-foreground" : "text-text-subtle"
                }`}
              >
                {s.label}
              </NavLink>
            ),
          )}
        </nav>
        <div ref={panel} className="min-w-0 flex-1 overflow-auto p-4 md:p-6">
          {active === "appearance" && <AppearanceSection />}
          {active === "alerts" && <AlertsSettingsSection card={Card} />}
          {active === "general" && <GeneralSection settings={GENERAL} />}
        </div>
      </div>
    </div>
  )
}
