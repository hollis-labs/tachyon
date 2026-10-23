import { buttonVariants } from '@hollis-labs/design-components'
import { Link } from 'react-router-dom'
import { isInternalHref, safeHref } from './policy'
import type { AlertLink } from './types'

const CLS = `${buttonVariants({ variant: 'outline', size: 'xs' })} font-mono uppercase tracking-label`

/** The alert's primary action in phase 1: a validated link. */
export function AlertLinkButton({ link, onFollow }: { link: AlertLink; onFollow?: () => void }) {
  const href = safeHref(link.href)
  if (!href) return null
  if (isInternalHref(href))
    return (
      <Link to={href} data-slot="button" className={CLS} onClick={onFollow}>
        {link.label}
      </Link>
    )
  return (
    <a href={href} data-slot="button" target="_blank" rel="noopener noreferrer" className={CLS} onClick={onFollow}>
      {link.label}
    </a>
  )
}
