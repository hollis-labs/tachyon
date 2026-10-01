# Services and health

All Services lists Cerberus connector definitions. A connector being live means
its connector is available; it does not prove that its managed services or
resources are healthy. Service Health keeps daemon state, connector liveness,
runtime service health and resource health separate. Missing or unrecognized
provider fields display as unknown rather than inferred success.

## Reads and refresh

The pages check the declared `service_list`, `service_read`, `service_status`
and `service_health` capabilities. Unsupported reads have an explicit message.
All Services offers local search by connector ID/resource type and filters by
resource type and liveness. Details show definition metadata, declared operations
and configuration field names/types/requirements. They do not show stored
credentials or invoke a connector operation.

Reads run on entry; subsequent refresh is manual. Repeated refreshes coalesce
while a read is in flight. Health can take about 90 seconds because the provider
makes three serial reads; after three seconds the page shows that timing hint.
There is no background health polling.

During refresh, health is Checking. After error or an operator-decision ask,
current health is Unknown; a previous healthy result is never presented as
current. The last successful refresh time remains labeled as a previous check
when appropriate. Detail metadata can retain a prior successful definition.
Errors and asks appear inline. Ask prompts/options are read-only; this page
submits no approval and never replays an operation.

These are snapshots, not live streams. See [host operations](../host-operations.md)
for the serial-wire and outer deadline model.
