import { useMemo, type ReactNode } from 'react'
import type { LinkTextSlotProps } from '@tachyon/host-sdk'
import { useApi } from '@tachyon/torque-client'
import { detect } from '../refs'
import { useAutolink } from './context'
import { createLiveResolvers } from './live'
import { AutolinkProvider } from './Provider'
import { createTaskDetailCache, TaskDetailContext } from './taskDetail'
import { createTesseractCache, TesseractContext } from './tesseractDetail'
import { createTesseractReadClient } from '../tesseract/client'
import { Linkified, type LinkifiedProps } from './Linkified'
import { isAutolinkSurfaceEnabled, type AutolinkSurface } from './surfaces'

export interface LinkTextProps extends Omit<LinkifiedProps, 'text'> {
  surface: AutolinkSurface
  text: string
}

/** Linkified for a named surface: plain text when that surface is opted out. */
export function LinkText({ surface, text, ...rest }: LinkTextProps) {
  const { registry } = useAutolink()
  const enabled = isAutolinkSurfaceEnabled(surface)
  const kindsKey = rest.kinds?.join(',')
  const { includeCode, kinds } = rest
  // Text with no references stays a bare text node (no wrapper span, no layout change).
  const hasRef = useMemo(
    () => enabled && detect(text, registry, { skipCode: !includeCode, ...(kinds ? { kinds } : {}) }).some((x) => x.type !== 'text'),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [enabled, text, registry, registry.version, includeCode, kindsKey],
  )
  if (!hasRef) return <>{text}</>
  return <Linkified text={text} {...rest} />
}

/** Mount once at the shell (inside ApiProvider and the router, above ConsoleShell). */
export function ShellAutolink({ children }: { children: ReactNode }) {
  const api = useApi()
  const tesseract = useMemo(() => createTesseractCache(createTesseractReadClient()), [])
  const resolvers = useMemo(() => createLiveResolvers(api, tesseract), [api, tesseract])
  const taskDetails = useMemo(() => createTaskDetailCache(api), [api])
  return (
    <TaskDetailContext.Provider value={taskDetails}>
      <TesseractContext.Provider value={tesseract}>
        <AutolinkProvider resolvers={resolvers}>{children}</AutolinkProvider>
      </TesseractContext.Provider>
    </TaskDetailContext.Provider>
  )
}

/** host-sdk `LinkText` slot: the autolinker exposed to other plugins (e.g. Planner comments). */
export function LinkTextSlot({ surface, text, includeCode, kinds }: LinkTextSlotProps) {
  return <LinkText surface={surface as AutolinkSurface} text={text} includeCode={includeCode} kinds={kinds} />
}
