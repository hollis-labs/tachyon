import assert from "node:assert/strict"
import { after, test } from "node:test"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  server: { middlewareMode: true, watch: null },
  appType: "custom",
})
const { setConfig, resetConfig, configWriteNotice } =
  await server.ssrLoadModule("/src/api/settings.ts")
after(() => server.close())

for (const [name, action, restartRequired, expected] of [
  [
    "no-op set",
    "save",
    false,
    "Settings saved. This command did not change settings that require a restart.",
  ],
  [
    "no-op reset",
    "reset",
    false,
    "Defaults restored. This command did not change settings that require a restart.",
  ],
  ["real set", "save", true, "Settings saved. Restart this plugin to apply these changes."],
  ["real reset", "reset", true, "Defaults restored. Restart this plugin to apply these changes."],
]) {
  test(`${name} presents the command's restart outcome`, async (t) => {
    const config = {
      target: { id: "example", settings: {} },
      values: {},
      validation: { valid: true },
    }
    t.mock.method(globalThis, "fetch", async (url, options) => {
      assert.equal(url, `/api/verb/config_${action === "save" ? "set" : "reset"}`)
      assert.deepEqual(
        JSON.parse(options.body),
        action === "save"
          ? { plugin: "example", values: { enabled: true } }
          : { plugin: "example" },
      )
      return new Response(
        JSON.stringify({
          status: "ok",
          data: { plugin: "example", restart_required: restartRequired, config },
        }),
      )
    })
    const result =
      action === "save"
        ? await setConfig("example", { enabled: true })
        : await resetConfig("example")
    assert.equal(result.status, "ok")
    assert.deepEqual(result.data.config, config)
    assert.equal(configWriteNotice(action, result.data.restart_required), expected)
  })
}
