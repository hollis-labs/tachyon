import { useState } from 'react'
import { AdminInput } from '@hollis-labs/kit-admin'
import { Field } from './AgentEditor'
import type { ObjectRecord } from './owned-builder-client'
import { ProviderActionReview } from './ProviderActionReview'
import { providerActionsClient } from './provider-actions'

export function AgentPublication({ agent, disabled }: { agent: ObjectRecord; disabled: boolean }) {
  const [provider, setProvider] = useState(''), [base, setBase] = useState('')
  return <ProviderActionReview objectID={agent.id} disabled={disabled || !provider.trim() || !base.trim()} preview={id => providerActionsClient.publish({ id, object_id: agent.id, expected_revision: agent.revision, provider, extends: base })}>
    <p className="dash-body text-fg-muted">Publish saved revision {agent.revision} as a separate Tether catalog profile. Save draft edits first. The chosen base supplies inherited permissions; project and machine placement stay in Tachyon. Team definition pins require a separate Tether definition mapping.</p>
    <Field label="Tether provider ID" htmlFor="publication-provider"><AdminInput id="publication-provider" value={provider} onChange={e => setProvider(e.target.value)} /></Field>
    <Field label="Tether base profile ID" htmlFor="publication-base"><AdminInput id="publication-base" value={base} onChange={e => setBase(e.target.value)} /></Field>
    <p className="dash-support text-fg-muted">Local tool declarations require an explicit MCP policy mapping before publication.</p>
  </ProviderActionReview>
}
