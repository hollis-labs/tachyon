import { describe, expect, it } from 'vitest'
import { parseTachHref } from './tach'

describe('parseTachHref', () => {
  it('names a Tesseract item by id, whatever the case of the scheme or service', () => {
    expect(parseTachHref('tach:tesseract/01M4SCJHDM8FV6MDPSDTMZ0X44')).toEqual({ kind: 'tesseract.item', canonical: '01M4SCJHDM8FV6MDPSDTMZ0X44' })
    expect(parseTachHref(`TACH://Tesseract/${'01M4SCJHDM8FV6MDPSDTMZ0X44'.toLowerCase()}`)).toEqual({ kind: 'tesseract.item', canonical: '01M4SCJHDM8FV6MDPSDTMZ0X44' })
  })
  it('names a Tesseract key with or without a domain, encoded or not', () => {
    const key = 'project/agent-os/workspace/handoff#approval_execution_2026_10_22'
    expect(parseTachHref(`tach:tesseract/${key}`)).toEqual({ kind: 'tesseract.key', canonical: key })
    expect(parseTachHref(`tach:tess/${encodeURIComponent(key)}`)).toEqual({ kind: 'tesseract.key', canonical: key })
    expect(parseTachHref(`tach:memory/workspace@${key}`)?.canonical).toBe(`workspace@${key}`)
  })
  it('names a Torque task', () => {
    expect(parseTachHref('tach:torque/CW-20261019-0028')).toEqual({ kind: 'torque.task', canonical: 'CW-20261019-0028' })
    expect(parseTachHref('tach:task/ep-20261019-0028')?.kind).toBe('torque.epic')
  })
  it('names an approval item', () => {
    const id = `item_${'0123456789abcdef'.repeat(2)}`
    expect(parseTachHref(`tach:approval/${id}`)).toEqual({ kind: 'approval', canonical: id })
    expect(parseTachHref('tach:approval/item_zz')).toBeNull()
  })
  it('names a card item', () => {
    const id = `item_${'0123456789abcdef'.repeat(2)}`
    expect(parseTachHref(`tach:card/${id}`)).toEqual({ kind: 'card', canonical: id })
    expect(parseTachHref('tach:card/CW-20261019-0028')).toBeNull()
  })
  it('refuses what it cannot name', () => {
    for (const h of [undefined, '', 'https://x.io', 'tach:tesseract/', 'tach:tesseract/just_a_key', 'tach:unknown/CW-20261019-0028', 'tach:torque/CW-1', 'tach:tesseract/%E0%A4%A']) {
      expect(parseTachHref(h)).toBeNull()
    }
  })
})
