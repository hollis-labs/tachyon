# Light-mode audit (L-E)

- `frontend/src/no-literals.test.ts` (runs in CI with the frontend tests): no hex / rgb() / palette-utility colours in app source. Exceptions are listed in the test with the reason.
- `node scripts/light-audit/audit.mjs <origin> [outDir]`: renders every route in light and dark at 1440 and 390, runs axe (WCAG A/AA incl. colour contrast), checks that `data-mode` follows the stored `mode`, and writes screenshots. Exit 1 on any violation. Run it against the live origin after each deploy.
- When `playwright` and `axe-core` are not installed in the repo, set `PLAYWRIGHT_MODULE` to the playwright package directory (or its `index.mjs`) and `AXE_JS` to `axe.min.js`.
- After the routes it audits states behind a click (the chat `?demo=cards` aside tabs, drawers, + menu and palette, and the Alerts tray). A state whose control is not on the page is printed as `skip` and does not fail the run. Add states to `STATES` in the script.
