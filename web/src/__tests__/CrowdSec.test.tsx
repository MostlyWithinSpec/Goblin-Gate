// Tests for Security → CrowdSec (task 105): the save must never wipe a stored
// bouncer key, the status card must show every state, and errors must be shown.
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate, CrowdSecSettings, CrowdSecStatus } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn(), getCrowdSecStatus: vi.fn() }
})

import * as api from '../api/objects'
import CrowdSec from '../pages/security/CrowdSec'
import { buildCrowdSec, validateCrowdSec, type CrowdSecForm } from '../pages/security/CrowdSec'

// The page polls (refetchInterval), so retry and refetch are off to keep the
// tests deterministic.
const wrap = (ui: React.ReactElement) => {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, refetchInterval: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

const form = (over: Partial<CrowdSecForm> = {}): CrowdSecForm => ({
  enabled: true,
  url: 'http://10.0.0.5:8080',
  api_key: '',
  poll: '30',
  scope: 'wan',
  hasKey: true,
  ...over,
})

const status = (over: Partial<CrowdSecStatus> = {}): CrowdSecStatus => ({
  enabled: true,
  connected: true,
  last_pull: new Date().toISOString(),
  active: 12,
  dropped_packets: 3456,
  ...over,
})

const candidate = (crowdsec?: CrowdSecSettings): Candidate => ({ crowdsec })

const lastPatch = (): [string, unknown] => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

const card = () => screen.getByTestId('crowdsec-status')

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(candidate())
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getCrowdSecStatus).mockReset().mockResolvedValue(status())
})

describe('buildCrowdSec', () => {
  test('a stored key the caller cannot see is left out of the PATCH body', () => {
    const built = buildCrowdSec(form(), { enabled: true, url: 'http://x', api_key: '' })
    expect(built).toEqual({ enabled: true, url: 'http://10.0.0.5:8080', scope: 'wan', poll_seconds: 30 })
    expect('api_key' in built).toBe(false)
  })

  test('a key the caller can see is sent back unchanged when not retyped', () => {
    const built = buildCrowdSec(form(), { enabled: true, url: 'http://x', api_key: 'adminkey' })
    expect(built.api_key).toBe('adminkey')
  })

  test('a retyped key wins over the stored one', () => {
    const built = buildCrowdSec(form({ api_key: ' fresh ' }), { enabled: true, api_key: 'old' })
    expect(built.api_key).toBe('fresh')
  })

  test('empty poll falls back to the server default and the scope is kept', () => {
    const built = buildCrowdSec(form({ poll: '', scope: 'all', hasKey: false }), undefined)
    expect(built.poll_seconds).toBe(10)
    expect(built.scope).toBe('all')
    expect(built.api_key).toBeUndefined()
  })
})

describe('validateCrowdSec', () => {
  test('an enabled bouncer needs a URL and a key unless one is stored', () => {
    expect(validateCrowdSec(form({ url: '', hasKey: false })).url).toMatch(/required/i)
    expect(validateCrowdSec(form({ hasKey: false })).api_key).toMatch(/cscli bouncers add/i)
    expect(validateCrowdSec(form())).toEqual({})
  })

  test('credentials in the URL and an out-of-range poll are rejected', () => {
    expect(validateCrowdSec(form({ url: 'http://u:p@10.0.0.5:8080' })).url).toMatch(/credentials/i)
    expect(validateCrowdSec(form({ url: '10.0.0.5' })).url).toMatch(/LAPI/i)
    expect(validateCrowdSec(form({ poll: '2' })).poll).toMatch(/5-300/)
    expect(validateCrowdSec(form({ poll: '' })).poll).toBeUndefined()
  })

  test('a disabled bouncer with no key at all still validates', () => {
    expect(validateCrowdSec(form({ enabled: false, url: '', api_key: '', hasKey: false }))).toEqual({})
  })
})

describe('CrowdSec page', () => {
  test('loads the stored settings and keeps the key when only the poll changes', async () => {
    // A caller that may not see secrets gets api_key back empty although a key
    // is stored (the URL proves it), so the PATCH must simply omit the field.
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, url: 'http://10.0.0.5:8080', api_key: '', poll_seconds: 30, scope: 'wan' }),
    )
    wrap(<CrowdSec />)

    await waitFor(() => expect(screen.getByDisplayValue('http://10.0.0.5:8080')).toBeTruthy())
    const key = screen.getByLabelText('Bouncer key') as HTMLInputElement
    expect(key.value).toBe('')
    expect(key.placeholder).toBe('unchanged')
    expect(key.type).toBe('password')
    expect((screen.getByLabelText('Poll interval') as HTMLInputElement).value).toBe('30')

    fireEvent.change(screen.getByLabelText('Poll interval'), { target: { value: '60' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('crowdsec')
    expect(value).toEqual({ enabled: true, url: 'http://10.0.0.5:8080', scope: 'wan', poll_seconds: 60 })
  })

  test('sends a typed key and reports "Saved."', async () => {
    // Nothing stored yet: the candidate has neither a URL nor a key.
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: false }))
    wrap(<CrowdSec />)
    await waitFor(() => expect(screen.getByLabelText('LAPI URL')).toBeTruthy())

    fireEvent.click(screen.getByLabelText('Enable CrowdSec'))
    fireEvent.change(screen.getByLabelText('LAPI URL'), { target: { value: 'https://cs.internal:8080' } })
    fireEvent.change(screen.getByLabelText('Bouncer key'), { target: { value: 'KEY123' } })
    fireEvent.change(screen.getByLabelText('Scope'), { target: { value: 'all' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    expect(value).toEqual({
      enabled: true,
      url: 'https://cs.internal:8080',
      scope: 'all',
      poll_seconds: 10,
      api_key: 'KEY123',
    })
    await waitFor(() => expect(within(card()).queryByText(/Last error/)).toBeNull())
    expect(await screen.findByText('Saved.')).toBeTruthy()
  })

  test('a key typed and saved is sent again on the next save, not dropped', async () => {
    // An admin sees the real key; after replacing it the page must echo the
    // new key, or the server would store an empty one.
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: true, url: 'http://10.0.0.5:8080', api_key: 'OLD' }))
    wrap(<CrowdSec />)
    await waitFor(() => expect(screen.getByLabelText('Bouncer key')).toBeTruthy())

    fireEvent.change(screen.getByLabelText('Bouncer key'), { target: { value: 'NEW' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Saved.')).toBeTruthy()
    expect((lastPatch()[1] as CrowdSecSettings).api_key).toBe('NEW')

    fireEvent.change(screen.getByLabelText('Poll interval'), { target: { value: '60' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledTimes(2))
    expect(lastPatch()[1]).toMatchObject({ api_key: 'NEW', poll_seconds: 60 })
  })

  test('shows a client-side error and does not PATCH', async () => {
    // A caller that may see secrets holds the real key, so an empty key field
    // is not an error here — only the poll interval is.
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, url: 'http://10.0.0.5:8080', api_key: 'adminkey', poll_seconds: 30 }),
    )
    wrap(<CrowdSec />)
    await waitFor(() => expect(screen.getByLabelText('LAPI URL')).toBeTruthy())

    fireEvent.change(screen.getByLabelText('Poll interval'), { target: { value: '600' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toMatchObject({ textContent: 'Choose 5-300 seconds.' })
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('shows validation errors coming back from the server', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'crowdsec.api_key', message: 'required when CrowdSec is enabled' }],
    })
    wrap(<CrowdSec />)
    await waitFor(() => expect(screen.getByLabelText('LAPI URL')).toBeTruthy())

    fireEvent.change(screen.getByLabelText('Bouncer key'), { target: { value: 'k' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/required when CrowdSec is enabled/)
    expect(screen.queryByText('Saved.')).toBeNull()
  })

  test('status card: connected shows the counters and the last pull', async () => {
    vi.mocked(api.getCrowdSecStatus).mockResolvedValue(status({ last_pull: new Date().toISOString() }))
    wrap(<CrowdSec />)

    await waitFor(() => expect(within(card()).getByText('Connected')).toBeTruthy())
    expect(within(card()).getByText('12')).toBeTruthy()
    expect(within(card()).getByText('3456')).toBeTruthy()
    expect(within(card()).getByText('just now')).toBeTruthy()
    expect(within(card()).queryByText(/Cannot read/)).toBeNull()
  })

  test('status card: enabled but not connected, with the last error', async () => {
    vi.mocked(api.getCrowdSecStatus).mockResolvedValue(
      status({ connected: false, active: 0, dropped_packets: 0, last_pull: undefined, last_error: 'LAPI 403 unauthorized' }),
    )
    wrap(<CrowdSec />)

    await waitFor(() => expect(within(card()).getByText('Not connected')).toBeTruthy())
    expect(within(card()).getByText('—')).toBeTruthy()
    expect(within(card()).getByRole('alert').textContent).toContain('LAPI 403 unauthorized')
  })

  test('status card: disabled', async () => {
    vi.mocked(api.getCrowdSecStatus).mockResolvedValue(
      status({ enabled: false, connected: false, active: 0, dropped_packets: 0, last_pull: undefined }),
    )
    wrap(<CrowdSec />)

    await waitFor(() => expect(within(card()).getByText('Disabled')).toBeTruthy())
  })

  test('status card: the endpoint is unavailable', async () => {
    vi.mocked(api.getCrowdSecStatus).mockRejectedValue(new Error('CrowdSec is not available'))
    wrap(<CrowdSec />)

    await waitFor(() => expect(within(card()).getByText('Unavailable')).toBeTruthy())
    expect(within(card()).getByRole('alert').textContent).toContain('CrowdSec is not available')
  })

  test('help panel shows the cscli command and the wiki link', async () => {
    wrap(<CrowdSec />)
    expect(await screen.findByText('cscli bouncers add goblin-gate')).toBeTruthy()
    const link = screen.getByRole('link', { name: /wiki/i }) as HTMLAnchorElement
    expect(link.href).toContain('Threat-Feeds-and-DNS-Filter')
  })
})
