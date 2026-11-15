import { createContext, useContext } from 'react'

/** True when the page header's overflow menu carries the reply filter, so the composer toolbar does not. */
export const HeaderFilterContext = createContext(false)
export const useFilterInHeader = () => useContext(HeaderFilterContext)
