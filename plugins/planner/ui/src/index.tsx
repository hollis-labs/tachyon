import type { TachyonPlugin } from "@tachyon/host-sdk"
import { PlannerApiProvider } from "@planner/api/context"
import { TachyonPlannerClient } from "@planner/api/client"
import { Compass } from "lucide-react"
import PlannerShell from "@planner/shell/PlannerShell"
import LensPage from "@planner/shell/lens-page"
import { Navigate, useParams } from "react-router-dom"
import { lensOfSlug, lensPath } from "@planner/lib/vocabulary"

/** Planner records and revisions are owned by the Tachyon host. */
const plannerClient = new TachyonPlannerClient()

function ShellRoute() {
  return (
    <PlannerApiProvider client={plannerClient}>
      <PlannerShell />
    </PlannerApiProvider>
  )
}

function PlannerRoute() {
  const { db: slug, id } = useParams<{ db?: string; id?: string }>()
  // The old pages' routes land on the lens or facet that replaced them.
  if (slug === undefined || slug === "board" || slug === "flight" || slug === "priorities") return <Navigate to="/planner/themes" replace />
  if (slug === "activity") return <Navigate to="/planner/themes?facet=need-you" replace />
  const db = lensOfSlug(slug)
  if (!db) return <Navigate to="/planner/themes" replace />
  // The database's own name (workstreams) is an alias of the lens slug (streams).
  if (slug !== lensPath(db).slice("/planner/".length)) return <Navigate to={lensPath(db, id === undefined ? undefined : decodeURIComponent(id))} replace />
  return (
    <PlannerApiProvider client={plannerClient}>
      {db === "themes" ? <PlannerShell /> : <LensPage key={db} db={db} />}
    </PlannerApiProvider>
  )
}

export const plannerPlugin: TachyonPlugin = {
  id: "planner",
  name: "Planner",
  nav: [{ key: "planner", label: "Planner", to: "/planner", icon: <Compass className="h-4 w-4" />, priority: 10 }],
  routes: [
    { path: "/planner/themes", element: <ShellRoute /> },
    { path: "/planner/themes/:id", element: <ShellRoute /> },
    { path: "/planner", element: <PlannerRoute /> },
    { path: "/planner/:db", element: <PlannerRoute /> },
    { path: "/planner/:db/:id", element: <PlannerRoute /> },
  ],
}
