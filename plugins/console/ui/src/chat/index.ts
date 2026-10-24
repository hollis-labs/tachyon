// Agent Console chat (C1). Import from here.
//
//   <ChatProvider client={createMailChatClient(messaging, { operator, agents })}>
//     <ChatDock open onClose={...} agent={recipient} />
//
// Wrap in AutolinkProvider (R1) at the shell so message refs resolve to chips.
export { ChatProvider, useChat } from './context'
export { ChatPanel, type ChatPanelProps } from './ChatPanel'
export { ChatDock, ChatButton } from './ChatDock'
export { ChatView, type ChatViewProps } from './view/ChatView'
export { ConversationView, type ConversationViewProps } from './view/ConversationView'
export { ChatPage } from './view/ChatPage'
export { ReplyFilterToggle } from './ReplyFilterToggle'
export { ThreadConversation } from './view/ThreadConversation'
export { ThreadView } from './ThreadView'
export { ToolChips } from './ToolChips'
export { CapabilityBadge } from './CapabilityBadge'
export { useChatThread, useChatThreads, useChatUnread } from './hooks'
export { applyChatEvent, mergeMessages, type ChatThreadState } from './reducer'
export { createAppAgentChatClient, isAppThread, isAppAgent } from './appagent'
export { createCompositeChatClient } from './composite'
export { createMailChatClient, chatMessageFromThread, MAIL_CAPABILITY, type MailChatOptions } from './mail'
export * from './types'
