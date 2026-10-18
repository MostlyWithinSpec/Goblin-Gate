import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate, LogExport } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getLogExportStatus: vi.fn(),
  }
})

import * as api from '../api/objects'
import LogExportPage from '../pages/log/Export'

const cfg: Candidate = {}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getLogExportStatus).mockResolvedValue([])
})

describe('Log export page', () => {
  it('adds a syslog entry with the right section body', async () => {
    wrap(<LogExportPage />)
    await screen.findByRole('button', { name: 'New' })
    fireEvent.click(screen.getByRole('button', { name: 'New' }))

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'rsyslog' } })
    fireEvent.change(screen.getByLabelText('Address'), { target: { value: '10.0.0.2:514' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('log_export')
    const arr = value as LogExport[]
    expect(arr).toHaveLength(1)
    expect(arr[0]).toMatchObject({ name: 'rsyslog', enabled: true, type: 'syslog', address: '10.0.0.2:514' })
  })

  it('adds a Loki entry with kinds and token', async () => {
    wrap(<LogExportPage />)
    await screen.findByRole('button', { name: 'New' })
    fireEvent.click(screen.getByRole('button', { name: 'New' }))

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'grafana' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'loki' } })
    await waitFor(() => expect(screen.getByLabelText('URL')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'https://loki.example.com/loki/api/v1/push' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'traffic' }))
    fireEvent.change(screen.getByLabelText(/Bearer token/i), { target: { value: 'tok-123' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('log_export')
    const arr = value as LogExport[]
    expect(arr[0]).toMatchObject({
      name: 'grafana',
      type: 'loki',
      url: 'https://loki.example.com/loki/api/v1/push',
      kinds: ['traffic'],
      token: 'tok-123',
    })
  })

  it('keeps the stored secrets when editing without new ones', async () => {
    // The server redacts secrets, so the candidate arrives with an empty password.
    const withEntry: Candidate = {
      ...cfg,
      log_export: [{ name: 'grafana', enabled: true, type: 'loki', url: 'https://loki.example.com/loki/api/v1/push' }],
    }
    vi.mocked(api.getCandidate).mockResolvedValue(withEntry)

    wrap(<LogExportPage />)
    await screen.findByRole('button', { name: 'grafana' })
    fireEvent.click(screen.getByRole('button', { name: 'grafana' }))

    // Secrets arrive redacted (empty); the placeholder says "unchanged".
    const pw = screen.getByLabelText(/Password/i) as HTMLInputElement
    expect(pw.value).toBe('')
    expect(pw.placeholder).toBe('unchanged')

    // Change only the URL and save.
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'https://loki2.example.com/loki/api/v1/push' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    const arr = value as LogExport[]
    expect(arr[0].url).toBe('https://loki2.example.com/loki/api/v1/push')
    // Empty secret is dropped by clean so the server keeps the stored one.
    expect(arr[0].password).toBeUndefined()
  })

  it('shows the status column: sent, dropped in warning colour, last error in red', async () => {
    const withEntry: Candidate = {
      ...cfg,
      log_export: [{ name: 'rsyslog', enabled: true, type: 'syslog', address: '10.0.0.2:514' }],
    }
    vi.mocked(api.getCandidate).mockResolvedValue(withEntry)
    vi.mocked(api.getLogExportStatus).mockResolvedValue([
      { name: 'rsyslog', sent: 42, dropped: 3, last_sent: new Date().toISOString(), last_error: 'connection refused' },
    ])

    wrap(<LogExportPage />)
    await screen.findByText('42')
    // The dropped count is inside a span with the warning colour.
    expect(screen.getByText('3').parentElement).toHaveClass('text-amber-600')
    expect(screen.getByText('connection refused')).toHaveClass('text-red-600')
  })

  it('shows validation messages for a bad syslog address', async () => {
    wrap(<LogExportPage />)
    await screen.findByRole('button', { name: 'New' })
    fireEvent.click(screen.getByRole('button', { name: 'New' }))

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'rsyslog' } })
    fireEvent.change(screen.getByLabelText('Address'), { target: { value: 'not-an-address' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    expect(await screen.findByText(/host:port/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })
})
