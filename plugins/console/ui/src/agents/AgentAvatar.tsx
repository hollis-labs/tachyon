import { IdentityAvatar } from '@hollis-labs/kit-admin'
import { useEffect, useState, useSyncExternalStore } from 'react'
import { avatarRevision, storedUrl } from './avatar-service'

const SIZE_CLASS = { definition: 'dash-identity-definition', large: 'dash-identity-large' } as const

/** The agent's stored avatar from the Station service; the in-browser robot stands in until one exists or if the service is unreachable. */
export function AgentAvatar({ identity, size }: { identity: string; size: keyof typeof SIZE_CLASS }) {
  const version = useSyncExternalStore(avatarRevision.subscribe, avatarRevision.get, avatarRevision.get)
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [identity, version])
  if (failed) return <IdentityAvatar identity={identity} size={size} />
  return <img src={storedUrl(identity, 'svg', version)} alt="Agent avatar" onError={() => setFailed(true)} loading="lazy" decoding="async"
    className={`${SIZE_CLASS[size]} shrink-0 object-contain [image-rendering:pixelated]`} />
}
