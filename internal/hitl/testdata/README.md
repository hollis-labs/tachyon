# Tangent HITL contract fixture

`tangent-hitl-request.schema.json` is an unchanged copy of Tangent's authored
`internal/envelope/extensions/packages/tangent.hitl/hitl-item/request.schema.json`
at commit `6c34d968c52fb680cc17a7af818ebcc3b5181c66`
(`v0.16.0-5-g6c34d96`). Wire contract version is 1.0.

The builder contract tests and fake MCP enqueue server validate exact request
JSON against this fixture; updating the contract requires reviewing the upstream
schema and adapting those requests. Tests do not read a mutable sibling checkout.
