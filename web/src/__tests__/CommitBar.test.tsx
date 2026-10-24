import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { commitConfig } from '../api/config'
import { CommitBar } from '../components/CommitBar'

// The real API module over a stubbed fetch: the commit must reach the server
// with the CSRF header (it was a bare fetch without it and failed silently).
type Call = { url: string; init?: RequestInit }
let calls: Call[]
let diffData: { count: number; changes: Array<{ path: string; old?: unknown; new?: unknown }> }
let statusData: { version: string; pending_confirm?: { deadline: string } | null }

beforeEach(() => {
  calls = []
  document.cookie = 'gg_csrf=tok123'
  diffData = { count: 1, changes: [{ path: 'system.hostname', old: 'a', new: 'b' }] }
  statusData = { version: 'x', pending_confirm: null }
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, init })
    if (url === '/api/v1/config/diff')
      return new Response(JSON.stringify(diffData), { status: 200 })
    if (url === '/api/v1/system/status')
      return new Response(JSON.stringify(statusData), { status: 200 })
    if (url === '/api/v1/commit')
      return new Response(JSON.stringify({ revision: 8 }), { status: 200 })
    if (url === '/api/v1/commit/confirm')
      return new Response(JSON.stringify({ status: 'confirmed' }), { status: 200 })
    return new Response('{}', { status: 200 })
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
    // Override fetch to return a 422 for commit.
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, init })
      if (url === '/api/v1/config/diff')
        return new Response(JSON.stringify(diffData), { status: 200 })
      if (url === '/api/v1/system/status')
        return new Response(JSON.stringify(statusData), { status: 200 })
      if (url === '/api/v1/commit')
        return new Response(
          JSON.stringify({
            error: {
              code: 'validation_failed',
              message: 'the config has errors; nothing was applied',
              details: [{ path: 'system.hostname', message: 'invalid hostname' }],
            },
          }),
          { status: 422 },
        )
      return new Response('{}', { status: 200 })
    }))
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

  test('sends confirm_minutes when auto-rollback is checked', async () => {
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    // Check the auto-rollback checkbox.
    const cb = screen.getByLabelText(/Roll back automatically/)
    fireEvent.click(cb)
    // Set minutes to 10.
    const minInput = screen.getByLabelText('Rollback minutes')
    fireEvent.change(minInput, { target: { value: '10' } })
    fireEvent.click(screen.getByRole('button', { name: 'Commit' }))
    expect(await screen.findByText('Committed revision 8')).toBeInTheDocument()
    const c = calls.find((x) => x.url === '/api/v1/commit')!
    expect(JSON.parse(c.init?.body as string)).toEqual({ comment: '', confirm_minutes: 10 })
  })

  test('does not send confirm_minutes when auto-rollback is unchecked', async () => {
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    // Ensure checkbox is unchecked (default for non-risky paths).
    const cb = screen.getByLabelText(/Roll back automatically/)
    expect(cb).not.toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: 'Commit' }))
    expect(await screen.findByText('Committed revision 8')).toBeInTheDocument()
    const c = calls.find((x) => x.url === '/api/v1/commit')!
    expect(JSON.parse(c.init?.body as string)).toEqual({ comment: '' })
  })

  test('pre-checks auto-rollback for a policies diff', async () => {
    diffData = { count: 1, changes: [{ path: 'policies[id=1].action', old: 'accept', new: 'deny' }] }
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    const cb = await screen.findByLabelText(/Roll back automatically/)
    expect(cb).toBeChecked()
    // Hint should be visible.
    expect(screen.getByText(/auto-rollback is recommended/)).toBeInTheDocument()
  })

  test('does not pre-check auto-rollback for a DNS-only diff', async () => {
    diffData = { count: 1, changes: [{ path: 'dns.local_records[0].address', old: '1.2.3.4', new: '5.6.7.8' }] }
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /1\s*Changes/ }))
    const cb = await screen.findByLabelText(/Roll back automatically/)
    expect(cb).not.toBeChecked()
  })

  test('banner shows countdown and Confirm calls confirmCommit', async () => {
    // Set up a pending confirmation with a deadline ~5 minutes from now.
    const deadline = new Date(Date.now() + 5 * 60 * 1000).toISOString()
    statusData = { version: 'x', pending_confirm: { deadline } }
    wrap()
    // The banner should appear with the countdown.
    const banner = await screen.findByRole('status')
    expect(banner).toHaveTextContent('Commit pending confirmation')
    // Countdown should show a time (e.g. "4:59" or similar).
    expect(banner).toHaveTextContent(/\d+:\d{2}/)
    // Click Confirm.
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => {
      const confirmCall = calls.find((x) => x.url === '/api/v1/commit/confirm')
      expect(confirmCall).toBeDefined()
      expect(confirmCall?.init?.method).toBe('POST')
    })
  })

  test('banner turns red in the last minute', async () => {
    const deadline = new Date(Date.now() + 30 * 1000).toISOString() // 30s from now
    statusData = { version: 'x', pending_confirm: { deadline } }
    wrap()
    const banner = await screen.findByRole('status')
    expect(banner.className).toContain('bg-red-600')
  })

  test('says confirmed, not rolled back, after Confirm', async () => {
    const deadline = new Date(Date.now() + 5 * 60 * 1000).toISOString()
    statusData = { version: 'x', pending_confirm: { deadline } }
    wrap()
    await screen.findByRole('button', { name: 'Confirm' })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(calls.some((x) => x.url === '/api/v1/commit/confirm')).toBe(true))
    statusData = { version: 'x', pending_confirm: null }
    await waitFor(() => expect(screen.queryByText('Commit confirmed')).toBeTruthy(), { timeout: 8000, interval: 500 })
    expect(screen.queryByText(/rolled back/)).toBeNull()
  })

  test('shows rolled-back message when pending_confirm disappears without confirm', async () => {
    // Start with a pending confirmation that is about to run out.
    const deadline = new Date(Date.now() + 1000).toISOString()
    statusData = { version: 'x', pending_confirm: { deadline } }

    wrap()
    // Wait for the banner to appear.
    await screen.findByRole('status')

    // Now simulate the deadline passing (pending_confirm goes to null).
    statusData = { version: 'x', pending_confirm: null }
    // The query refetches every 1s; wait for the rolled-back message.
    await waitFor(
      () => {
        expect(screen.queryByText(/rolled back/)).toBeTruthy()
      },
      { timeout: 8000, interval: 500 },
    )
  })
})

describe('commitConfig', () => {
  test('sends confirm_minutes only when set', async () => {
    await commitConfig('x', 5)
    expect(JSON.parse(calls[0].init?.body as string)).toEqual({ comment: 'x', confirm_minutes: 5 })
  })
})
