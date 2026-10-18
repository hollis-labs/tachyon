import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@hollis-labs/design-components'
import { useSyncExternalStore } from 'react'
import { MockIdentityConfirm, type IdentityConfirm } from '@console/seams/identity-confirm'
import { Btn, Chip, Heading } from './ui'

function MockStepUp({ identity }: { identity: MockIdentityConfirm }) {
  const pending = useSyncExternalStore(identity.subscribe, () => identity.pending(), () => identity.pending())
  const head = pending[0]
  if (!head) return null
  const { request: r } = head
  const ttl = Math.round((r.ttlMs ?? 60_000) / 1000)
  return (
    <Dialog open onOpenChange={open => { if (!open) identity.resolve(head.id, 'cancelled', 'dismissed') }}>
      <DialogContent widthClassName="max-w-md">
        <DialogHeader>
          <DialogTitle>Confirm your identity</DialogTitle>
          <DialogDescription>This change needs a fresh confirmation. Nothing happens until you confirm.</DialogDescription>
        </DialogHeader>
        <dl className="space-y-3 px-5 py-4 text-control">
          <div>
            <Heading>Who</Heading>
            <dd className="text-fg">{r.who?.label ?? r.who?.id ?? 'You (signed-in user)'}</dd>
          </div>
          <div>
            <Heading>What</Heading>
            <dd className="text-fg">{r.what.title}</dd>
            {r.what.summary && <dd className="text-fg-muted">{r.what.summary}</dd>}
          </div>
          <div>
            <Heading>Scope</Heading>
            <dd className="flex flex-wrap gap-1.5">{r.scope.map((s) => <Chip key={s} tone={s.includes('privileged') || s.includes('grant') ? 'border-warning/60 text-warning' : undefined}>{s}</Chip>)}</dd>
          </div>
          <div>
            <Heading>Expires</Heading>
            <dd className="text-fg-muted">The grant is valid for {ttl} seconds after you confirm.</dd>
          </div>
        </dl>
        <div className="flex items-center gap-2 border-t border-border px-5 py-3">
          <span className="mr-auto font-mono text-caption uppercase tracking-widest text-fg-faint">mock step-up</span>
          <Btn autoFocus onClick={() => identity.resolve(head.id, 'cancelled', 'cancelled by user')}>Cancel</Btn>
          <Btn tone="primary" onClick={() => identity.resolve(head.id, 'granted')}>Confirm</Btn>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** Renders the step-up prompt for the mock; a real IdentityConfirm brings its own UI, so this renders nothing for it. */
export function StepUpHost({ identity }: { identity: IdentityConfirm }) {
  return identity instanceof MockIdentityConfirm && identity.mode === 'manual' ? <MockStepUp identity={identity} /> : null
}
