import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ChevronLeft } from 'lucide-react'
import { PrWorkspace } from './PrWorkspace'
import { PR_INBOX_PATH } from '../inbox/href'
import { prRefFromParams } from './route'

/** Route element for PR_WORKSPACE_ROUTE. `?file=` deep-links a file; `?readonly=1` hides writes. */
export default function PrWorkspacePage() {
  const params = useParams()
  const [search] = useSearchParams()
  const prRef = prRefFromParams(params)
  const navigate = useNavigate()
  // Arrived from the inbox, a chip or a link: Back returns there. A fresh load falls back to the planner.
  const canGoBack = useLocation().key !== 'default'
  if (!prRef) {
    return (
      <div className="p-6" role="alert">
        <h1 className="font-mono text-sm uppercase tracking-widest text-danger">Invalid pull request link</h1>
      </div>
    )
  }
  return (
    <PrWorkspace
      key={`${prRef.repo.host}/${prRef.repo.owner}/${prRef.repo.name}/${prRef.number}`}
      className="h-full"
      prRef={prRef}
      initialFile={search.get('file') ?? undefined}
      readOnly={search.get('readonly') === '1'}
      leading={
        <Link to={PR_INBOX_PATH} onClick={canGoBack ? (e) => { e.preventDefault(); navigate(-1) } : undefined} className="inline-flex items-center gap-1 text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
          <ChevronLeft className="size-3" /> Pull requests
        </Link>
      }
    />
  )
}
