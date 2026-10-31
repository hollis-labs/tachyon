#!/usr/bin/env node
// L-E render audit: every route in light (and dark) at 1440 and 390: axe color-contrast (WCAG AA 4.5:1 / 3:1 large),
// the other WCAG A/AA rules, and a screenshot per route/mode/width. Exits 1 when any violation is found.
//
//   node scripts/light-audit/audit.mjs https://tachyon.nanite.cloud [outDir]
//
// Needs `playwright` and `axe-core` resolvable (npm i -D playwright axe-core, or set PLAYWRIGHT_MODULE / AXE_JS).
import { createRequire } from 'node:module'
import { mkdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const base = (process.argv[2] ?? 'http://127.0.0.1:5179').replace(/\/$/, '')
const out = process.argv[3] ?? 'light-audit-out'
const require = createRequire(import.meta.url)
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const axeSrc = readFileSync(process.env.AXE_JS ?? require.resolve('axe-core/axe.min.js'), 'utf8')
mkdirSync(out, { recursive: true })

// Routes that exist in every build; the nav rail adds the rest (discovered from its links).
const ROUTES = ['/chat', '/planner', '/agents', '/approvals', '/repos', '/dashboards/transcripts', '/builder/agents', '/builder/launches', '/builder/library/prompts', '/registry/projects', '/settings/appearance', '/settings/general', '/settings/alerts', '/user']
const WIDTHS = [1440, 390]
const MODES = ['light', 'dark']

const browser = await chromium.launch()
let failures = 0
for (const mode of MODES) for (const width of WIDTHS) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: mode })
  await ctx.addInitScript((m) => { try { localStorage.setItem('mode', m) } catch {} }, mode)
  const page = await ctx.newPage()
  await page.goto(base + '/', { waitUntil: 'load' }).catch(() => undefined)
  await page.waitForTimeout(1500)
  const nav = await page.evaluate(() => [...document.querySelectorAll('nav a[href^="/"]')].map((a) => a.getAttribute('href')))
  for (const route of [...new Set([...ROUTES, ...nav])]) {
    await page.goto(base + route, { waitUntil: 'load' }).catch(() => undefined)
    await page.waitForTimeout(2500)
    await page.waitForFunction(() => document.documentElement.lang && document.documentElement.getAttribute('data-mode'), null, { timeout: 15000 }).catch(() => undefined)
    const resolved = await page.evaluate(() => document.documentElement.getAttribute('data-mode'))
    await page.addScriptTag({ content: axeSrc })
    const violations = await page.evaluate(async () => (await window.axe.run(document, { runOnly: ['wcag2a', 'wcag2aa'] })).violations.map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.length, first: v.nodes[0].target.join(' ').slice(0, 100), why: (v.nodes[0].any[0]?.message ?? '').slice(0, 140) })))
    const slug = route.replace(/[^a-z0-9]+/gi, '_').replace(/^_|_$/g, '') || 'root'
    await page.screenshot({ path: join(out, `${slug}-${mode}-${width}.png`) })
    const tag = `${mode} ${width} ${route}`
    if (resolved !== mode) { failures++; console.log(`FAIL ${tag}: data-mode is ${resolved}, expected ${mode}`) }
    if (violations.length) { failures++; console.log(`FAIL ${tag}`); for (const v of violations) console.log(`  ${v.impact} ${v.id} x${v.nodes} ${v.first}\n    ${v.why}`) }
    else console.log(`ok   ${tag}`)
  }
  await ctx.close()
}
await browser.close()
process.exit(failures ? 1 : 0)
