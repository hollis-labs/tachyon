import {
  Activity,
  ClipboardList,
  GitBranch,
  LayoutDashboard,
  Radio,
  Rocket,
  Server,
  Settings,
  Terminal,
  Users,
} from "lucide-react"
import type { ComponentType } from "react"

export interface IconProps {
  className?: string
}

/**
 * Documented bounded Lucide icon vocabulary for Tachyon navigation groups.
 *
 * Plugins declare an icon name in their nav group capability declaration.
 * This vocabulary maps kebab-case Lucide icon names (and existing alias names)
 * to their respective Lucide React icon components.
 * Unknown icon names safely fall back to Activity with a diagnostic warning.
 */
export const iconMap: Record<string, ComponentType<IconProps>> = {
  // Observability & default fallback
  activity: Activity,

  // SCM / source control (scm-ops uses "git-branch")
  git: GitBranch,
  "git-branch": GitBranch,

  // Work ops (work-ops uses "clipboard-list")
  clipboard: ClipboardList,
  "clipboard-list": ClipboardList,

  // Execution / launches (launch-ops uses "play")
  play: Rocket,
  rocket: Rocket,

  // Host dashboard
  dashboard: LayoutDashboard,
  "layout-dashboard": LayoutDashboard,

  // Telemetry / events
  radio: Radio,

  // Services (service-ops uses "server")
  server: Server,

  // Settings (config-ops uses "settings")
  settings: Settings,

  // Sessions (session-ops uses "terminal")
  terminal: Terminal,

  // Agents (agent-ops uses "users")
  users: Users,
}

export const SUPPORTED_ICON_NAMES: readonly string[] = Object.keys(iconMap)

export function isSupportedIcon(name?: string): name is string {
  return typeof name === "string" && Object.hasOwn(iconMap, name)
}

/**
 * Resolves a nav group icon by name. Falls back to Activity if the icon is unknown, undefined,
 * or an inherited object property.
 */
export function getNavIcon(name?: string): ComponentType<IconProps> {
  if (!name) {
    return Activity
  }
  if (isSupportedIcon(name)) {
    return iconMap[name]
  }
  console.warn(`[tachyon:nav] Unknown icon "${name}", falling back to Activity`)
  return Activity
}
