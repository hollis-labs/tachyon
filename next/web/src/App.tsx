import { ManifestShell, type PageComponent } from "@hollis-labs/app-shell"
import { Toaster, TooltipProvider } from "@hollis-labs/design-components"
import { ApiKeysPage, IDENTITY_PAGES, IdentityGate } from "@hollis-labs/kit-identity"
import { IdentityProvider, useIdentity } from "@hollis-labs/kit-identity/client"
import {
  Bot,
  GitPullRequest,
  Hammer,
  KeyRound,
  Library,
  LogOut,
  Map as MapIcon,
  MessageSquare,
  Monitor,
  ScrollText,
  Settings,
  ShieldCheck,
  User,
} from "lucide-react"
import { useEffect, useState } from "react"
import { BrowserRouter, useLocation, useNavigate } from "react-router-dom"
import { CapabilitiesProvider } from "./api/capabilities"
import { UNAUTHORIZED_EVENT } from "./api/request"
import { ChatSurface } from "./console/surface"
import { manifest, surfaceRoutes } from "./manifest"
import { SettingsPage } from "./pages/settings"
import { StubPage } from "./pages/stub"
import { PlannerSurface } from "./planner/surface"
import { KEY_SCOPES } from "./scopes"
import { LANDING, legacyRedirect, surfacePath } from "./surfaces"

// Kit icon names used by the contributions (next/cmd/tachyon/ui/app.json), resolved by the shell.
const icons = {
  map: MapIcon,
  bot: Bot,
  hammer: Hammer,
  library: Library,
  "message-square": MessageSquare,
  "shield-check": ShieldCheck,
  "scroll-text": ScrollText,
  "git-pull-request": GitPullRequest,
  settings: Settings,
  user: User,
  "key-round": KeyRound,
  monitor: Monitor,
  "log-out": LogOut,
}

// Page bodies by surface key. The area pages are stubs until their slice ports them (G2+); each
// resolves its own deep links from the URL with react-router.
const pages: Record<string, PageComponent> = {
  planner: () => <PlannerSurface />,
  agents: () => <StubPage area="Agent Ops" slice="G3" />,
  builder: () => <StubPage area="Builder" slice="G3" />,
  registry: () => <StubPage area="Registry" slice="G3" />,
  chat: () => <ChatSurface />,
  approvals: () => <StubPage area="Approvals" slice="G4" />,
  transcripts: () => <StubPage area="Transcripts" slice="G3" />,
  repos: () => <StubPage area="Repos" slice="G3" />,
  settings: () => <SettingsPage />,
  user: () => <IDENTITY_PAGES.account />,
  "identity-keys": () => <ApiKeysPage scopeSuggestions={KEY_SCOPES} />,
  "identity-sessions": () => <IDENTITY_PAGES.sessions />,
  "identity-sign-out": () => <IDENTITY_PAGES.signOut />,
}

// An explicit sign-out leaves the URL on the sign-out page; signing in again must not land there.
const SIGN_OUT_PATH = "/identity/sign-out"

/**
 * The kit moves to its "session expired" sign-in when one of its own requests is answered 401.
 * The app's own requests report the same fact through UNAUTHORIZED_EVENT; this asks the kit a
 * question only a live session answers, so its 401 puts the gate on the sign-in screen.
 */
function SessionLossBridge() {
  const { client } = useIdentity()
  useEffect(() => {
    const onLoss = () => void client.listSessions().catch(() => {})
    window.addEventListener(UNAUTHORIZED_EVENT, onLoss)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onLoss)
  }, [client])
  return null
}

/**
 * The app-shell, with the browser location bridged to the shell's path and navigate. It mounts once
 * a session exists, so a URL still on the sign-out page (an explicit sign-out, then sign-in) goes
 * home instead.
 */
function Shell() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const [fromSignOut] = useState(() => pathname === SIGN_OUT_PATH)
  const redirect = fromSignOut && pathname === SIGN_OUT_PATH ? LANDING : legacyRedirect(pathname)
  useEffect(() => {
    if (redirect) navigate(redirect, { replace: true })
  }, [redirect, navigate])
  if (redirect) return null
  return (
    <ManifestShell
      manifest={manifest}
      apiBase="/api"
      path={surfacePath(pathname, surfaceRoutes)}
      onNavigate={(to) => navigate(to)}
      pages={pages}
      icons={icons}
    />
  )
}

export function App() {
  return (
    // The gate renders first-run, sign-in and the no-owner screen outside the shell and mounts
    // the app only once a session exists.
    <IdentityProvider>
      <IdentityGate appName="tachyon">
        <SessionLossBridge />
        <CapabilitiesProvider>
          {/* As the old frontend/src/App.tsx: surfaces report write failures, undo and refusals as toasts. */}
          <TooltipProvider>
            <BrowserRouter>
              <Shell />
            </BrowserRouter>
            <Toaster position="bottom-right" />
          </TooltipProvider>
        </CapabilitiesProvider>
      </IdentityGate>
    </IdentityProvider>
  )
}
