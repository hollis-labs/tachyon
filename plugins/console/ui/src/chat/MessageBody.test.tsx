/** @vitest-environment jsdom */
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
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
