// Port (G5): the old Teams tab ran team operations through the builder domain (owned builder records, provider
// actions, the agent editor: agents/TeamOperations and its clients). They arrive with the Builder surface; until
// then the tab says so. The old file is plugins/console/ui/src/agents-monitor/teams-tab.tsx at main 99c7b88.
export function TeamsTab() {
  return (
    <p className="dash-body text-fg-muted">
      Team runs move to the Builder surface. Open Builder to start or follow a team.
    </p>
  )
}
