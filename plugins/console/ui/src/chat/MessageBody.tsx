import { Children, memo, type ReactNode } from 'react'
import { ChatMarkdown, type StreamdownProps } from '@hollis-labs/kit-chat/markdown'
import { Linkified } from '../autolink'

const refs = (children: ReactNode) =>
  Children.map(children, (child) => (typeof child === 'string' ? <Linkified text={child} /> : child))
const components: StreamdownProps['components'] = {
  strong: ({ children }) => <strong>{children}</strong>,
  p: ({ children }) => <p>{refs(children)}</p>,
  li: ({ children }) => <li>{refs(children)}</li>,
  a: ({ children, href }) =>
    href && /^(https?:|mailto:)/i.test(href) ? (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  img: ({ alt }) => <span className="text-zinc-500">{alt ? `[Image: ${alt}]` : '[Image]'}</span>,
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
