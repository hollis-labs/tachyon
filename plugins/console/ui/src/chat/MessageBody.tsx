import { Children, isValidElement, memo, useEffect, useRef, type ReactNode } from 'react'
import { ChatMarkdown, type StreamdownProps } from '@hollis-labs/kit-chat/markdown'
import { RefChip } from '../autolink/RefChip'
import { linkifiedMarkdownComponents } from '../autolink/markdown'
import { parseTachHref } from '../refs/tach'

// Every element that holds prose is linkified, not just p and li: ids lead bullets as
// **bold** or *emphasis*, and also sit in headings, quotes and table cells.
/** The visible text of a link's children, used as the label until the reference resolves. */
function plainText(node: ReactNode): string {
  return Children.toArray(node)
    .map((c) => (typeof c === 'string' || typeof c === 'number' ? String(c) : isValidElement<{ children?: ReactNode }>(c) ? plainText(c.props.children) : ''))
    .join('')
    .trim()
}

const components: StreamdownProps['components'] = {
  ...(linkifiedMarkdownComponents('messaging') as StreamdownProps['components']),
  a: ({ children, href }) => {
    // `[label](tach:service/id-or-key)`: the author names the reference, so nothing depends on pattern matching.
    const ref = parseTachHref(href)
    if (ref) return <RefChip kind={ref.kind} canonical={ref.canonical} fallbackLabel={plainText(children) || undefined} />
    return href && /^(https?:|mailto:)/i.test(href) ? (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    )
  },
  img: ({ alt }) => <span className="text-fg-muted">{alt ? `[Image: ${alt}]` : '[Image]'}</span>,
}
/** Shared portfolio renderer; raw HTML is excluded and remote images never load. */
export const MessageBody = memo(function MessageBody({
  body,
  streaming = false,
}: {
  body: string
  streaming?: boolean
}) {
  const root = useRef<HTMLDivElement>(null)
  // Wide tables and code blocks scroll inside the message; a scroller must be reachable by keyboard (axe scrollable-region-focusable).
  useEffect(() => {
    const el = root.current
    if (!el) return
    const mark = () => {
      for (const n of el.querySelectorAll<HTMLElement>('[class*="overflow-x-auto"], [class*="overflow-y-auto"], [class*="overflow-auto"]')) {
        if ((n.scrollWidth > n.clientWidth || n.scrollHeight > n.clientHeight) && !n.hasAttribute('tabindex')) {
          n.tabIndex = 0
          n.setAttribute('role', 'region')
          n.setAttribute('aria-label', 'Scrollable content')
        }
      }
    }
    mark()
    const mo = new MutationObserver(mark)
    mo.observe(el, { childList: true, subtree: true })
    return () => mo.disconnect()
  }, [body])
  return (
    <div ref={root} data-chat-prose className="mt-2 min-w-0">
      <ChatMarkdown
        streaming={streaming}
        controls={false}
        rehypePlugins={[]}
        skipHtml
        components={components}
        className="tachyon-chat-prose"
      >
        {body}
      </ChatMarkdown>
    </div>
  )
})
