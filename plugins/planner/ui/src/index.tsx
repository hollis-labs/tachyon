import type { TachyonPlugin } from "@tachyon/host-sdk"
import { KanbanSquare } from "lucide-react"

function PlannerPlaceholder() {
  return <div className="p-6 text-sm text-muted-foreground">Planner port in progress.</div>
}

export const plannerPlugin: TachyonPlugin = {
  id: "planner",
  name: "Planner",
  nav: [{ key: "planner", label: "Planner", to: "/planner", icon: <KanbanSquare className="h-4 w-4" />, priority: 10 }],
  routes: [
    { path: "/planner", element: <PlannerPlaceholder /> },
    { path: "/planner/:db", element: <PlannerPlaceholder /> },
    { path: "/planner/:db/:id", element: <PlannerPlaceholder /> },
  ],
}
