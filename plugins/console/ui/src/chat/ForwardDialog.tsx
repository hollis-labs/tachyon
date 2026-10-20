import { useEffect, useState } from 'react'
import { ForwardMessageDialog } from '@hollis-labs/kit-chat'
import { useAgentsClient } from '../agents/context'
import { useChat } from './context'
import { fetchChatAgents } from './roster'
import { forwardBody, forwardProblem, forwardTargets, type ForwardTarget } from './forward'
import type { ChatMessage } from './types'

export function ForwardDialog({ message, sourceLabel, onClose, onSent }: { message: ChatMessage; sourceLabel: string; onClose: () => void; onSent: (label: string) => void }) {
  const { client } = useChat()
  const agents = useAgentsClient()
  const [targets, setTargets] = useState<ForwardTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let live = true
    setLoading(true)
    Promise.allSettled([fetchChatAgents(), agents.listAgents()]).then(([roster, directory]) => {
      if (!live) return
      setTargets(forwardTargets(roster.status === 'fulfilled' ? roster.value : [], directory.status === 'fulfilled' ? directory.value : []))
      setLoadError(roster.status === 'rejected' || directory.status === 'rejected' ? 'Some recipients could not be loaded. Retry to search the full directory.' : null)
      setLoading(false)
    })
    return () => { live = false }
  }, [agents, retry])
  return <ForwardMessageDialog source={message.body} sourceLabel={sourceLabel} recipients={targets} loading={loading} loadError={loadError} onRetry={() => setRetry(v => v + 1)} onClose={onClose}
    validate={(id, note) => { const target = targets.find(t => t.id === id); return target ? forwardProblem(target, forwardBody(message, sourceLabel, note)) : null }}
    onSend={async (id, note) => {
      const target = targets.find(t => t.id === id)
      if (!target) throw new Error('This recipient is no longer available. Choose another agent.')
      const body = forwardBody(message, sourceLabel, note)
      const problem = forwardProblem(target, body)
      if (problem) throw new Error(problem)
      const thread = await client.openThread(target.recipient)
      if (thread.capability.canSend === false) throw new Error('This agent cannot accept a message right now. Try again when its turn finishes.')
      await client.send(thread.id, body)
      onSent(target.label)
    }} />
}
