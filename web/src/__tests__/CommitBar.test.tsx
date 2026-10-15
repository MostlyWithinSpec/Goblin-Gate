import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { commitConfig } from '../api/config'
import { CommitBar } from '../components/CommitBar'

// The real API module over a stubbed fetch: the commit must reach the server
// with the CSRF header (it was a bare fetch without it and failed silently).
type Call = { url: string; init?: RequestInit }
let calls: Call[]
let commitAnswer: Response

beforeEach(() => {
  calls = []
  document.cookie = 'gg_csrf=tok123'
  commitAnswer = new Response(JSON.stringify({ revision: 8 }), { status: 200 })
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, init })
    if (url === '/api/v1/config/diff') {
      return new Response(JSON.stringify({ count: 1, changes: [{ path: 'system.hostname', old: 'a', new: 'b' }] }))
    }
    if (url === '/api/v1/system/status') {
      return new Response(JSON.stringify({ version: 'x', pending_confirm: null }))
    }
    if (url === '/api/v1/commit') return commitAnswer
    return new Response('{}')
  }))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const wrap = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <div style={{ backdropFilter: 'blur(4px)' }}>
        <CommitBar />
      </div>
    </QueryClientProvider>,
  )

describe('CommitBar', () => {
  test('commits with the CSRF token and the comment', async () => {
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    fireEvent.change(screen.getByLabelText('Commit comment'), { target: { value: 'rename' } })
    fireEvent.click(screen.getByRole('button', { name: 'Commit' }))
    expect(await screen.findByText('Committed revision 8')).toBeInTheDocument()
    const c = calls.find((x) => x.url === '/api/v1/commit')!
    expect(c.init?.method).toBe('POST')
    expect((c.init?.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok123')
    expect(JSON.parse(c.init?.body as string)).toEqual({ comment: 'rename' })
  })

  test('shows validation errors instead of failing silently', async () => {
    commitAnswer = new Response(JSON.stringify({ error: { code: 'validation_failed', message: 'the config has errors; nothing was applied',
      details: [{ path: 'system.hostname', message: 'invalid hostname' }] } }), { status: 422 })
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Commit' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('the config has errors')
    expect(alert).toHaveTextContent('system.hostname: invalid hostname')
  })

  test('the dialog is rendered at the page root, not inside the header', async () => {
    const { container } = wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    const dialog = await screen.findByRole('dialog')
    expect(container.contains(dialog)).toBe(false)
    expect(dialog.parentElement).toBe(document.body)
  })
})

describe('commitConfig', () => {
  test('sends confirm_minutes only when set', async () => {
    await commitConfig('x', 5)
    expect(JSON.parse(calls[0].init?.body as string)).toEqual({ comment: 'x', confirm_minutes: 5 })
  })
})
