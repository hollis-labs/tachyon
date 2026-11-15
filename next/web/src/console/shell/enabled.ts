import { createContext, useContext } from 'react'

/** The Console is always on in Tachyon (live mode); kept as a context so call sites stay unchanged. */
export const ConsoleEnabledContext = createContext(true)

export function useConsoleEnabled(): boolean {
  return useContext(ConsoleEnabledContext)
}
