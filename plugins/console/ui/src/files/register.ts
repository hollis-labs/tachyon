import { FileCode, FileImage, FileJson, Globe } from 'lucide-react'
import { registerRefChipIcon, registerRefChrome, registerRefDetail, registerRefIcon, registerRefOwnHover } from '../autolink/extensions'
import { CasterViewerPane } from './CasterViewerPane'
import { FileHover } from './FileHover'
import { chipIconToken, FileViewerPane } from './FileViewerPane'

let registered = false
/** Idempotent: path references open a file viewer in the ref drawer. */
export function registerFileRefs(): void {
  if (registered) return
  registered = true
  registerRefDetail('path', FileViewerPane)
  registerRefChrome('path')
  registerRefOwnHover('path', FileHover)
  registerRefIcon('file-code', FileCode)
  registerRefIcon('file-image', FileImage)
  registerRefIcon('file-json', FileJson)
  registerRefIcon('file-html', Globe)
  registerRefChipIcon('path', (ref) => chipIconToken(ref.canonical))
  registerRefDetail('caster', CasterViewerPane)
}
