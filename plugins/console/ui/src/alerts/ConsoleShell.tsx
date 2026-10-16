import type { ReactNode } from 'react'
import { ConsoleProvider } from '../events/provider'
import type { ConsoleSource } from '../events/source'
import { ConsoleEnabledContext } from '../shell/enabled'
import { ConsoleIntegration } from '../shell/ConsoleIntegration'

/**
 * The single shell mount point for the Console. Place around the app shell,
 * inside ApiProvider and the router. In Tachyon the Console is always on and
 * always live (no `?console=` gating, no fixture source). It also mounts
 * messaging, chat dock, card kinds (shell/ConsoleIntegration).
 */
export function ConsoleShell({ children, source }: { children: ReactNode; source?: ConsoleSource }) {
  return (
    <ConsoleEnabledContext.Provider value>
      <ConsoleProvider source={source}>
        <ConsoleIntegration>{children}</ConsoleIntegration>
      </ConsoleProvider>
    </ConsoleEnabledContext.Provider>
  )
}
