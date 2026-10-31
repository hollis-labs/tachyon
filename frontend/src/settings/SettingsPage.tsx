import { AlertsSettingsSection } from "@tachyon/console-ui"
import { useEffect, useMemo, useRef } from "react"
import { NavLink, useNavigate, useParams, useSearchParams } from "react-router-dom"
import { AppearanceSection } from "./AppearanceSection"
import { GeneralSection } from "./GeneralSection"
import { devStubApi, realApi } from "./general"
import { resolveSection, SETTINGS_SECTIONS } from "./sections"

const useGeneralApi = () => {
  const [params] = useSearchParams()
  const dev = import.meta.env.DEV ? params.get("settings-stub") : null
  return useMemo(() => (dev ? devStubApi(dev) : realApi()), [dev])
}

const ITEM = "block whitespace-nowrap rounded-control px-3 py-[7px] text-xs leading-normal sm:mx-2"
const idle = "text-fg-muted hover:bg-selection"
const current = "bg-selection text-fg"

export function SettingsPage() {
  const { section } = useParams()
  const navigate = useNavigate()
  const active = resolveSection(section)
  const api = useGeneralApi()
  const panel = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (section !== active) navigate(`/settings/${active}`, { replace: true })
  }, [section, active, navigate])

  // Land with focus on the section heading.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-run when the section changes
  useEffect(() => {
    panel.current?.querySelector<HTMLElement>("h2")?.focus()
  }, [active])

  const sections = SETTINGS_SECTIONS.map((s) =>
    s.soon ? (
      <span key={s.id} aria-disabled="true" className={`${ITEM} max-sm:hidden opacity-50`}>
        {s.label} <span className="text-fg-muted">soon</span>
      </span>
    ) : (
      <NavLink
        key={s.id}
        to={`/settings/${s.id}`}
        aria-current={active === s.id ? "page" : undefined}
        className={`${ITEM} max-sm:mx-0 ${active === s.id ? current : idle}`}
      >
        {s.label}
      </NavLink>
    ),
  )

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between gap-3 border-b border-border-subtle px-4">
        <h1 className="font-mono text-caption font-semibold uppercase leading-none tracking-label text-fg">
          Settings
        </h1>
        <span className="text-label text-fg-muted">
          {active === "appearance" ? "Saved on this device" : "Applies to this Tachyon"}
        </span>
      </header>
      <div className="flex min-h-0 flex-1 flex-col sm:flex-row">
        <nav
          aria-label="Settings sections"
          className="flex shrink-0 gap-1 overflow-x-auto border-b border-border-subtle p-2 sm:w-[200px] sm:flex-col sm:gap-0 sm:overflow-visible sm:border-b-0 sm:border-r sm:p-0"
        >
          <p className="px-3 pb-1.5 pt-3 font-mono text-caption uppercase leading-none tracking-label text-fg-muted max-sm:hidden">
            Settings
          </p>
          {sections}
        </nav>
        <div
          ref={panel}
          className="flex min-w-0 flex-1 justify-center overflow-auto p-6 max-sm:p-3.5"
        >
          <div className="w-full max-w-[680px]">
            {active === "appearance" && <AppearanceSection />}
            {active === "alerts" && <AlertsSettingsSection />}
            {active === "general" && <GeneralSection api={api} />}
          </div>
        </div>
      </div>
    </div>
  )
}
