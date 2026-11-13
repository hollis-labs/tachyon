#!/usr/bin/env node
// L-E render audit: every route in light (and dark) at 1440 and 390: axe color-contrast (WCAG AA 4.5:1 / 3:1 large),
// the other WCAG A/AA rules, and a screenshot per route/mode/width. Exits 1 when any violation is found.
//
//   node scripts/light-audit/audit.mjs https://tachyon.nanite.cloud [outDir] [--sweep]
//
// Needs `playwright` and `axe-core` resolvable (npm i -D playwright axe-core, or set PLAYWRIGHT_MODULE / AXE_JS).
import { createRequire } from 'node:module'
import { existsSync, mkdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const args = process.argv.slice(2).filter((a) => !a.startsWith('--'))
// --sweep: also click every tab and open every menu or popup in the page body of each route (1440 only; slow).
const SWEEP = process.argv.includes('--sweep')
const base = (args[0] ?? 'http://127.0.0.1:5179').replace(/\/$/, '')
const out = args[1] ?? 'light-audit-out'
const require = createRequire(import.meta.url)
// PLAYWRIGHT_MODULE may be a package name, the package directory, or its entry file.
function playwrightEntry() {
  const m = process.env.PLAYWRIGHT_MODULE
  if (!m || !existsSync(m)) return m ?? 'playwright'
  return pathToFileURL(statSync(m).isDirectory() ? join(m, 'index.mjs') : m).href
}
const pw = await import(playwrightEntry())
const chromium = pw.chromium ?? pw.default?.chromium
if (!chromium) throw new Error('PLAYWRIGHT_MODULE does not export chromium: point it at the playwright package directory or its index.mjs')
const axeSrc = readFileSync(process.env.AXE_JS ?? require.resolve('axe-core/axe.min.js'), 'utf8')
mkdirSync(out, { recursive: true })

// Routes that exist in every build; the nav rail adds the rest (discovered from its links).
const ROUTES = ['/chat', '/planner', '/planner/themes', '/planner/streams', '/planner/roadmap', '/planner/ideas', '/planner/decisions', '/planner/risks', '/planner/inbox', '/agents', '/approvals', '/repos', '/dashboards/transcripts', '/builder/agents', '/builder/launches', '/builder/library/prompts', '/registry/projects', '/settings/appearance', '/settings/general', '/settings/alerts', '/user']
const WIDTHS = [1440, 390]
// States behind a click, audited after the routes: [name, route, steps]. A state whose control is absent (no data,
// narrow layout) is reported as "skip", not as a failure. Nothing here writes: the chat states use the ?demo=cards fixtures.
const DEMO = '/chat/app-tachyon?demo=cards'
const details = async (p) => { const d = p.getByRole('button', { name: 'Details' }).first(); if (await d.isVisible().catch(() => false)) await d.click() }
const asideTab = (name) => async (p) => { if (!(await p.getByRole('tab', { name: new RegExp('^' + name) }).first().isVisible().catch(() => false))) await details(p); await p.getByRole('tab', { name: new RegExp('^' + name) }).first().click({ timeout: 5000 }) }
const drawer = (name) => (p) => p.getByRole('button', { name, exact: true }).click({ timeout: 5000 })
const STATES = [
  ['chat cards', DEMO, async () => undefined],
  ...['Plan', 'Topics', 'Bookmarks', 'Work', 'Activity', 'Approvals', 'Files', 'Doc'].map((t) => [`chat aside ${t}`, DEMO, asideTab(t)]),
  ...['Scratchpad', 'Terminal', 'Workspace'].map((d) => [`chat drawer ${d}`, DEMO, drawer(d)]),
  ['chat + menu', DEMO, (p) => p.getByRole('button', { name: 'Add', exact: true }).click({ timeout: 5000 })],
  ['chat palette', DEMO, async (p) => { await p.getByRole('textbox', { name: 'Message', exact: true }).click({ timeout: 5000 }); await p.keyboard.press('/') }],
  ['alerts tray', '/agents', (p) => p.getByRole('button', { name: /^Alerts/ }).first().click({ timeout: 5000 })],
]
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
  const targets = [...new Set([...ROUTES, ...nav])].map((route) => ({ tag: route, route, act: null }))
  for (const [name, route, act] of STATES) targets.push({ tag: `[${name}]`, route, act })
  for (const { tag: label, route, act } of targets) {
    // A route can hard-navigate once (redirects, session connect): retry until the app document is there.
    for (let attempt = 0; attempt < 3; attempt++) {
      await page.goto(base + route, { waitUntil: 'load' }).catch(() => undefined)
      await page.waitForTimeout(2500)
      if (await page.evaluate(() => !!document.documentElement.lang).catch(() => false)) break
    }
    await page.waitForFunction(() => document.documentElement.lang && document.documentElement.getAttribute('data-mode'), null, { timeout: 15000 }).catch(() => undefined)
    if (act) {
      const reached = await act(page).then(() => true, () => false)
      if (!reached) { console.log(`skip ${mode} ${width} ${label}`); continue }
      await page.waitForTimeout(700)
    }
    const resolved = await page.evaluate(() => document.documentElement.getAttribute('data-mode'))
    await page.addScriptTag({ content: axeSrc })
    const violations = await page.evaluate(async () => (await window.axe.run(document, { runOnly: ['wcag2a', 'wcag2aa'] })).violations.map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.length, first: v.nodes[0].target.join(' ').slice(0, 100), why: (v.nodes[0].any[0]?.message ?? '').slice(0, 140) })))
    const slug = label.replace(/[^a-z0-9]+/gi, '_').replace(/^_|_$/g, '') || 'root'
    await page.screenshot({ path: join(out, `${slug}-${mode}-${width}.png`) })
    const tag = `${mode} ${width} ${label}`
    if (resolved !== mode) { failures++; console.log(`FAIL ${tag}: data-mode is ${resolved}, expected ${mode}`) }
    if (violations.length) { failures++; console.log(`FAIL ${tag}`); for (const v of violations) console.log(`  ${v.impact} ${v.id} x${v.nodes} ${v.first}\n    ${v.why}`) }
    else console.log(`ok   ${tag}`)
  }
  if (SWEEP && width === 1440) {
    const check = async (tag) => {
      await page.addScriptTag({ content: axeSrc })
      const v = await page.evaluate(async () => (await window.axe.run(document, { runOnly: ['wcag2a', 'wcag2aa'] })).violations.map((x) => `  ${x.impact} ${x.id} x${x.nodes.length} ${x.nodes[0].target.join(' ').slice(0, 100)}\n    ${(x.nodes[0].any[0]?.message ?? '').slice(0, 140)}`))
      if (v.length) { failures++; console.log(`FAIL ${mode} ${width} ${tag}\n${v.join('\n')}`) } else console.log(`ok   ${mode} ${width} ${tag}`)
    }
    // Only opens things: a tab is selected, a popup is opened and closed with Escape. Nothing inside a popup is clicked.
    for (const route of [...new Set([...ROUTES, ...nav])]) {
      for (const [kind, sel, max] of [['tab', 'main [role=tab]', 8], ['popup', 'main [aria-haspopup]', 4]]) {
        await page.goto(base + route, { waitUntil: 'load' }).catch(() => undefined)
        await page.waitForTimeout(2500)
        const count = Math.min(await page.locator(sel).count(), max)
        for (let i = 0; i < count; i++) {
          const el = page.locator(sel).nth(i)
          const name = ((await el.getAttribute('aria-label').catch(() => null)) ?? (await el.innerText().catch(() => ''))).replace(/\s+/g, ' ').slice(0, 24)
          if (!(await el.click({ timeout: 3000 }).then(() => true, () => false))) continue
          await page.waitForTimeout(700)
          await check(`${route} ${kind}:${name}`)
          if (kind === 'popup') { await page.keyboard.press('Escape'); await page.waitForTimeout(300) }
        }
      }
    }
  }
  await ctx.close()
}
await browser.close()
process.exit(failures ? 1 : 0)
