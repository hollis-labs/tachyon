import { memo } from 'react'
import { ChatMarkdown, type StreamdownProps } from '@hollis-labs/kit-chat/markdown'
import { linkifiedMarkdownComponents } from '../autolink/markdown'

// Every element that holds prose is linkified, not just p and li: ids lead bullets as
// **bold** or *emphasis*, and also sit in headings, quotes and table cells.
const components: StreamdownProps['components'] = {
  ...(linkifiedMarkdownComponents('messaging') as StreamdownProps['components']),
  a: ({ children, href }) =>
    href && /^(https?:|mailto:)/i.test(href) ? (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
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
  return (
    <div data-chat-prose className="mt-2 min-w-0">
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
