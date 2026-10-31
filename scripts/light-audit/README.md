# Light-mode audit (L-E)

- `frontend/src/no-literals.test.ts` (runs in CI with the frontend tests): no hex / rgb() / palette-utility colours in app source. Exceptions are listed in the test with the reason.
- `node scripts/light-audit/audit.mjs <origin> [outDir]`: renders every route in light and dark at 1440 and 390, runs axe (WCAG A/AA incl. colour contrast), checks that `data-mode` follows the stored `mode`, and writes screenshots. Exit 1 on any violation. Run it against the live origin after each deploy.
