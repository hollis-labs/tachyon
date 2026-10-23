/** @vitest-environment jsdom */
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { AutolinkProvider } from '../autolink'
import { createDefaultRefRegistry } from '../refs'
import { MessageBody } from './MessageBody'
afterEach(cleanup)
it('renders headings, lists, emphasis, code and tables with the portfolio markdown renderer', () => {
  const { container } = render(
    <MessageBody
      body={
        '## Status\n\nA **ready** agent.\n\n- First\n- Second\n\n```go\nfmt.Println("hello")\n```\n\n| Name | State |\n| --- | --- |\n| Agent | Ready |'
      }
    />,
  )
  expect(screen.getByRole('heading', { name: 'Status' })).toBeTruthy()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(container.querySelector('strong')?.textContent).toBe('ready')
  expect(container.querySelector('pre')?.textContent).toContain('fmt.Println')
  expect(screen.getByRole('table')).toBeTruthy()
})
it('does not execute HTML, fetch remote images, or activate unsafe URLs', () => {
  const { container } = render(
    <MessageBody
      body={
        '<script>alert(1)</script>\n\n<img src="https://example.test/tracker" onerror="alert(1)">\n\n![diagram](https://example.test/image.png)\n\n[bad](javascript:alert(1))\n\n[docs](https://example.test/docs)'
      }
    />,
  )
  expect(container.querySelector('script,img,iframe')).toBeNull()
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull()
  expect(screen.getByRole('link', { name: 'docs' }).getAttribute('rel')).toContain('noopener')
})
it('links ids wherever markdown puts them, including bold and emphasis at the start of a bullet', () => {
  const body = [
    '- CW-20261016-0101 plain bullet',
    '- **CW-20261016-0102** bold lead-in',
    '- *CW-20261016-0103* emphasised',
    '1. **CW-20261016-0104**: numbered',
    '- [ ] **CW-20261016-0105** task item',
    '> **CW-20261016-0106** quoted',
    '### CW-20261016-0107',
    '| id |\n| --- |\n| **CW-20261016-0108** |',
  ].join('\n\n')
  const { container } = render(
    <MemoryRouter>
      <AutolinkProvider registry={createDefaultRefRegistry()}>
        <MessageBody body={body} />
      </AutolinkProvider>
    </MemoryRouter>,
  )
  const found = [...container.querySelectorAll('[data-ref-kind]')].map((c) => c.textContent)
  for (let n = 101; n <= 108; n++) expect(found).toContain(`CW-20261016-0${n}`)
})
it('renders a tach: link as a reference chip, labelled until it resolves, and leaves unknown schemes as text', () => {
  const body = [
    '[Handoff: approval execution](tach:tesseract/01M4SCJHDM8FV6MDPSDTMZ0X44)',
    '[the decision](tach:tesseract/project/agent-os/workspace/decisions#approved_action_executor)',
    '[CW-20261019-0028](tach:torque/CW-20261019-0028)',
    '[nope](tach:unknown/whatever)',
    '[bad](javascript:alert(1))',
  ].join('\n\n')
  const { container } = render(
    <MemoryRouter>
      <AutolinkProvider registry={createDefaultRefRegistry()}>
        <MessageBody body={body} />
      </AutolinkProvider>
    </MemoryRouter>,
  )
  const chips = [...container.querySelectorAll('[data-ref-kind]')]
  expect(chips.map((c) => c.getAttribute('data-ref-kind'))).toEqual(['tesseract.item', 'tesseract.key', 'torque.task'])
  expect(chips[0].textContent).toBe('Handoff: approval execution')
  expect(chips[1].textContent).toBe('the decision')
  expect(container.textContent).toContain('nope')
  expect(container.querySelector('a[href^="tach:"], a[href^="javascript:"]')).toBeNull()
})
it('renders an approval link as a chip that opens the approvals page for that item', () => {
  const id = `item_${'0123456789abcdef'.repeat(2)}`
  const { container } = render(
    <MemoryRouter>
      <AutolinkProvider registry={createDefaultRefRegistry()}>
        <MessageBody body={`[Approval: cerberus.resource.reload on hub](tach:approval/${id})`} />
      </AutolinkProvider>
    </MemoryRouter>,
  )
  const chip = container.querySelector('[data-ref-kind="approval"]')
  expect(chip?.textContent).toBe('Approval: cerberus.resource.reload on hub')
  expect(chip?.getAttribute('href')).toBe(`/approvals?item=${id}`)
})

it('renders a card link as a chip that opens the review dialog route for that item', () => {
  const id = `item_${'0123456789abcdef'.repeat(2)}`
  const { container } = render(
    <MemoryRouter>
      <AutolinkProvider registry={createDefaultRefRegistry()}>
        <MessageBody body={`[Card: apply 2 status changes](tach:card/${id})`} />
      </AutolinkProvider>
    </MemoryRouter>,
  )
  const chip = container.querySelector('[data-ref-kind="card"]')
  expect(chip?.textContent).toBe('Card: apply 2 status changes')
  expect(chip?.getAttribute('href')).toBe(`/approvals?item=${id}`)
})
