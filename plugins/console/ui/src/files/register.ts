import { registerRefChrome, registerRefDetail } from '../autolink/extensions'
import { CasterViewerPane } from './CasterViewerPane'
import { FileViewerPane } from './FileViewerPane'

let registered = false
/** Idempotent: path references open a file viewer in the ref drawer. */
export function registerFileRefs(): void {
  if (registered) return
  registered = true
  registerRefDetail('path', FileViewerPane)
  registerRefChrome('path')
  registerRefDetail('caster', CasterViewerPane)
}
