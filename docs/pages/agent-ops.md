# Agent Operations notifications and focus

The shell mounts one design-kit Toaster. Agent Operations notifications sent
through the kit, including launch success/failure, appear as toast notifications. Other pages retain
their own inline feedback; a global Toaster does not replace every error or
status message.

Agent details and the New Agent wizard share the large management dialog.
The initiating control is captured before an asynchronous detail read so closing
the dialog can return focus to that control. The New Agent wizard returns focus
to New Agent. LargeDialog and AgentManageDialog forward an optional final-focus
reference to the kit dialog primitive.

At narrow mobile widths, Agent Operations and durable-agent rows wrap long
names, badges and metadata, while keeping trailing actions available. The
desktop layout retains its existing arrangement. Durable-agent catalogs remain
read-only; their presence does not expose start/stop lifecycle controls.

The frontend consumes released design-components 0.1.1. Button/Pill sizing and
foreground behavior come from that release; success/info Pill tone support
remains an upstream limitation. See [README](../../README.md) for dependencies
and capability-based page registration.
