/** Settings sections, in list order. `soon` sections are listed but not reachable. */
export interface SettingsSection {
  id: string
  label: string
  soon?: boolean
}

export const SETTINGS_SECTIONS: readonly SettingsSection[] = [
  { id: "appearance", label: "Appearance" },
  { id: "general", label: "General" },
  { id: "agents", label: "Agents", soon: true },
]

export const DEFAULT_SECTION = "appearance"

export function resolveSection(id: string | undefined): string {
  const hit = SETTINGS_SECTIONS.find((s) => s.id === id && !s.soon)
  return hit ? hit.id : DEFAULT_SECTION
}
