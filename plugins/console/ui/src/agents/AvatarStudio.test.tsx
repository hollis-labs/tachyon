/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { AvatarStudio } from './AvatarStudio'
import { avatarName, createAvatarClient, previewUrl, storedUrl, type AvatarClient } from './avatar-service'

afterEach(cleanup)

const client = (): AvatarClient & { save: ReturnType<typeof vi.fn>; clear: ReturnType<typeof vi.fn> } =>
  ({ save: vi.fn(async () => ({ ok: true as const })), clear: vi.fn(async () => ({ ok: true as const })) })

it('builds preview and stored image urls from the knobs', () => {
  const url = new URL(previewUrl({ seed: 'agt_1', fill: 40, colors: { kind: 'custom', accent: '#FF6B5B', bg: '#1F2A44' } }, 'png', 512))
  expect(url.pathname).toBe('/img/preview')
  expect(Object.fromEntries(url.searchParams)).toEqual({ seed: 'agt_1', fill: '40', format: 'png', accent: 'ff6b5b', bg: '1f2a44', size: '512' })
  expect(new URL(previewUrl({ seed: 'a', fill: 65, colors: { kind: 'slot', slot: 'violet' } })).searchParams.get('palette')).toBe('violet')
  expect(new URL(previewUrl({ seed: 'a', fill: 65, colors: { kind: 'seed' } })).searchParams.has('palette')).toBe(false)
  expect(avatarName('a/b:c')).toBe('a_b_c')
  expect(storedUrl('agt_1', 'svg', 3)).toMatch(/\/avatars\/agt_1\.svg\?v=3$/)
})

it('saves colours and fill for the agent urn, and only for that face', async () => {
  const c = client()
  render(<AvatarStudio urn="agt_1" name="Worker" open onClose={() => undefined} client={c} />)
  fireEvent.click(screen.getByRole('button', { name: 'violet' }))
  fireEvent.change(screen.getByLabelText(/^Fill/), { target: { value: '30' } })
  fireEvent.click(screen.getByRole('button', { name: 'Set avatar' }))
  await screen.findByText('Avatar set.')
  expect(c.save).toHaveBeenCalledWith('agt_1', { seed: 'agt_1', fill: 30, colors: { kind: 'slot', slot: 'violet' } })

  fireEvent.change(screen.getByLabelText('Explore a different face'), { target: { value: 'something else' } })
  expect((screen.getByRole('button', { name: 'Set avatar' }) as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByText(/Preview only/)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: "Back to the agent's face" }))
  expect((screen.getByRole('button', { name: 'Set avatar' }) as HTMLButtonElement).disabled).toBe(false)
})

it('clears the saved avatar and reports a backend that is not connected', async () => {
  const c = client()
  render(<AvatarStudio urn="agt_1" name="Worker" open onClose={() => undefined} client={c} />)
  fireEvent.click(screen.getByRole('button', { name: 'Clear' }))
  await screen.findByText(/Avatar cleared/)
  expect(c.clear).toHaveBeenCalledWith('agt_1')

  const missing = createAvatarClient(async () => new Response('', { status: 404 }))
  const shell = createAvatarClient(async () => new Response('<!doctype html>', { status: 200, headers: { 'content-type': 'text/html' } }))
  expect(await shell.clear('agt_1')).toMatchObject({ ok: false, unavailable: true })
  const saved = createAvatarClient(async () => Response.json({ ok: true }))
  expect(await saved.clear('agt_1')).toEqual({ ok: true })
  expect(await missing.save('agt_1', { seed: 'agt_1', fill: 65, colors: { kind: 'seed' } })).toMatchObject({ ok: false, unavailable: true })
})
