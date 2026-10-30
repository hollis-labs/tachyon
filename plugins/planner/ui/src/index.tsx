import type { TachyonPlugin } from "@tachyon/host-sdk"
import { PlannerApiProvider } from "@planner/api/context"
import { TachyonPlannerClient } from "@planner/api/client"
import { Compass } from "lucide-react"
import PlannerPage from "@planner/pages/PlannerPage"
import PlannerShell from "@planner/shell/PlannerShell"

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
  return (
    <PlannerApiProvider client={plannerClient}>
      <PlannerPage />
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
