import type { TachyonPlugin } from "@tachyon/host-sdk"
import { PlannerApiProvider } from "@planner/api/context"
import { TachyonPlannerClient } from "@planner/api/client"
import { Compass } from "lucide-react"
import PlannerPage from "@planner/pages/PlannerPage"
import PlannerShell from "@planner/shell/PlannerShell"
import LensPage from "@planner/shell/lens-page"
import { useParams } from "react-router-dom"
import { isPlannerDb } from "@planner/lib/planner"

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
  const { db } = useParams<{ db?: string }>()
  // The six other lenses open on the shell; /planner, /planner/flight, board and activity keep the earlier page until their routes are retired.
  return (
    <PlannerApiProvider client={plannerClient}>
      {isPlannerDb(db) && db !== "themes" ? <LensPage key={db} db={db} /> : <PlannerPage />}
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
