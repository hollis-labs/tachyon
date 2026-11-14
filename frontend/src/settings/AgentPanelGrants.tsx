import { Button } from "@hollis-labs/design-components"
import { useSyncExternalStore } from "react"
import { alwaysGrants, resetAllGrants, resetGrant, subscribeGrants } from "@console/chat/aside/grants"
import { SetCard, SetRow } from "./parts"

// useSyncExternalStore needs a stable snapshot: the list as one string.
const snapshot = () => JSON.stringify(alwaysGrants())

/** Agents allowed to change the Agent Panel view without asking, per agent and chat. Reset puts the question back. */
export function AgentPanelGrants() {
  const list: string[] = JSON.parse(useSyncExternalStore(subscribeGrants, snapshot, () => "[]"))
  return (
    <SetCard label="Agent Panel">
      <SetRow
        title="Agents that may change the panel"
        help="You chose Always when an agent asked to change a view you had changed. Reset to be asked again."
      >
        {list.length > 0 && (
          <Button type="button" variant="outline" onClick={resetAllGrants}>
            Reset all
          </Button>
        )}
      </SetRow>
      {list.length === 0 ? (
        <SetRow sub help="No agent has permission yet." />
      ) : (
        list.map((scope) => {
          const [agent, thread] = scope.split("|")
          return (
            <SetRow key={scope} sub title={agent} help={`in ${thread}`}>
              <Button type="button" variant="outline" aria-label={`Reset ${agent} in ${thread}`} onClick={() => resetGrant(scope)}>
                Reset
              </Button>
            </SetRow>
          )
        })
      )}
    </SetCard>
  )
}
