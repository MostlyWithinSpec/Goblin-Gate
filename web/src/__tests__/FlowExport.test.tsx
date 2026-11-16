// Tests for Log & Report → Flow export (task 145): the form saves the whole
// flow_export section, the server's validation errors land under their field,
// and the status line tells running from error from "Commit to start".
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate, FlowExportSettings, FlowExportStatus } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn(), getFlowExportStatus: vi.fn() }
})

import * as api from '../api/objects'
import { t } from '../i18n'
import FlowExportPage, { buildFlow, formOf, validateFlow, type FlowExportForm } from '../pages/log/FlowExport'

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

const form = (over: Partial<FlowExportForm> = {}): FlowExportForm => ({
  enabled: true,
  collector: '10.0.0.5:2055',
  protocol: 'ipfix',
  timeout: '60',
  ...over,
})

const status = (over: Partial<FlowExportStatus> = {}): FlowExportStatus => ({
  enabled: true,
  collector: '10.0.0.5:2055',
  protocol: 'ipfix',
  running: true,
  packets_sent: 7,
  records_sent: 42,
  open_flows: 3,
  ...over,
})

const candidate = (flow_export?: FlowExportSettings): Candidate => ({ flow_export })

const lastPatch = (): [string, unknown] => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

const card = () => screen.getByTestId('flow-export-status')

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(candidate())
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getFlowExportStatus).mockReset().mockResolvedValue(status())
})

describe('buildFlow', () => {
  test('the defaults (IPFIX, 60 s) are left out of the section body', () => {
    expect(buildFlow(form())).toEqual({ enabled: true, collector: '10.0.0.5:2055' })
  })

  test('NetFlow v9 and a non-default timeout are stored', () => {
    expect(buildFlow(form({ protocol: 'netflow9', timeout: '120' }))).toEqual({
      enabled: true,
      collector: '10.0.0.5:2055',
      protocol: 'netflow9',
      active_timeout_seconds: 120,
    })
  })

  test('a blank collector and timeout save as nothing extra', () => {
    expect(buildFlow(form({ enabled: false, collector: '  ', timeout: '' }))).toEqual({ enabled: false })
  })

  test('formOf reads the stored section back with its defaults', () => {
    expect(formOf({ enabled: true, collector: 'collector.internal:4739', protocol: 'netflow9', active_timeout_seconds: 30 })).toEqual({
      enabled: true,
      collector: 'collector.internal:4739',
      protocol: 'netflow9',
      timeout: '30',
    })
    expect(formOf(undefined)).toEqual({ enabled: false, collector: '', protocol: 'ipfix', timeout: '60' })
  })
})

describe('validateFlow', () => {
  test('a clean form validates', () => {
    expect(validateFlow(form())).toEqual({})
    expect(validateFlow(form({ enabled: false, collector: '' }))).toEqual({})
    expect(validateFlow(form({ collector: 'collector.internal:4739' }))).toEqual({})
    expect(validateFlow(form({ collector: '[fd00::5]:4739' }))).toEqual({})
  })

  test('an enabled export wants a collector in host:port form', () => {
    expect(validateFlow(form({ collector: '' })).collector).toBe(t('flowExport.errCollectorRequired'))
    expect(validateFlow(form({ collector: 'collector.internal' })).collector).toMatch(/host:port/)
    expect(validateFlow(form({ collector: 'not an address' })).collector).toMatch(/host:port/)
  })

  test('the active timeout must be 10-600 seconds', () => {
    expect(validateFlow(form({ timeout: '5' })).timeout).toMatch(/10-600/)
    expect(validateFlow(form({ timeout: '601' })).timeout).toMatch(/10-600/)
    expect(validateFlow(form({ timeout: '' })).timeout).toBeUndefined()
  })
})

describe('Flow export page', () => {
  test('saves the section and says to commit', async () => {
    wrap(<FlowExportPage />)
    await waitFor(() => expect(screen.getByLabelText('Collector (host:port)')).toBeTruthy())

    fireEvent.click(screen.getByLabelText('Enable flow export'))
    fireEvent.change(screen.getByLabelText('Collector (host:port)'), { target: { value: '10.0.0.5:2055' } })
    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'netflow9' } })
    fireEvent.change(screen.getByLabelText('Active timeout (seconds)'), { target: { value: '120' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('flow_export')
    expect(value).toEqual({ enabled: true, collector: '10.0.0.5:2055', protocol: 'netflow9', active_timeout_seconds: 120 })
    expect(await screen.findByText('Saved. Use the commit bar to apply.')).toBeTruthy()
  })

  test('a client-side error is shown and nothing is saved', async () => {
    wrap(<FlowExportPage />)
    await waitFor(() => expect(screen.getByLabelText('Collector (host:port)')).toBeTruthy())

    fireEvent.click(screen.getByLabelText('Enable flow export'))
    fireEvent.change(screen.getByLabelText('Collector (host:port)'), { target: { value: 'collector.internal' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toMatchObject({ textContent: /host:port/ })
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('a validation error from the server shows under its field', async () => {
    // The patch body must pass the client-side rules to reach the server at all,
    // so here it is the collector the server objects to.
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'flow_export.collector', message: 'collector name has no usable address' }],
    })
    wrap(<FlowExportPage />)
    await waitFor(() => expect(screen.getByLabelText('Collector (host:port)')).toBeTruthy())

    fireEvent.change(screen.getByLabelText('Collector (host:port)'), { target: { value: 'collector.internal:4739' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/collector name has no usable address/)
    expect(screen.queryByText(/commit bar/)).toBeNull()
  })

  test('a field both sides check shows the client message and never PATCHes', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'flow_export.active_timeout_seconds', message: 'want 10-600 seconds' }],
    })
    wrap(<FlowExportPage />)
    await waitFor(() => expect(screen.getByLabelText('Active timeout (seconds)')).toBeTruthy())

    // 900 is out of range for both sides; the client rule stops the save, so the
    // page never PATCHes and shows its own wording for the same field.
    fireEvent.change(screen.getByLabelText('Active timeout (seconds)'), { target: { value: '900' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toMatchObject({ textContent: 'Choose 10-600 seconds.' })
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('status: running shows the counters and the last sent time', async () => {
    vi.mocked(api.getFlowExportStatus).mockResolvedValue(status({ last_sent: new Date().toISOString() }))
    wrap(<FlowExportPage />)

    await waitFor(() => expect(within(card()).getByText('Running')).toBeTruthy())
    expect(within(card()).getByText('42')).toBeTruthy()
    expect(within(card()).getByText('7')).toBeTruthy()
    expect(within(card()).getByText('just now')).toBeTruthy()
    expect(within(card()).getByText('10.0.0.5:2055')).toBeTruthy()
    expect(within(card()).queryByText(/Last error/)).toBeNull()
  })

  test('status: the last error shows in red', async () => {
    vi.mocked(api.getFlowExportStatus).mockResolvedValue(status({ last_error: 'connection refused' }))
    wrap(<FlowExportPage />)

    await waitFor(() => expect(within(card()).getByText('Running')).toBeTruthy())
    const err = within(card()).getByTestId('flow-export-last-error')
    expect(err).toHaveClass('text-red-600')
    expect(err).toHaveTextContent('Last error: connection refused')
  })

  test('status: saved but not running says "Commit to start"', async () => {
    // The exporter runs with the feature off, so it reports enabled: false and
    // nothing sent.
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: false }))
    vi.mocked(api.getFlowExportStatus).mockResolvedValue(status({ enabled: false, running: false, records_sent: 0, packets_sent: 0 }))
    wrap(<FlowExportPage />)

    await waitFor(() => expect(within(card()).getByTestId('flow-export-counters')).toBeTruthy())
    // Enable together with a collector: an enabled export needs one, and the
    // client-side check would otherwise stop the save.
    fireEvent.click(screen.getByLabelText('Enable flow export'))
    fireEvent.change(screen.getByLabelText('Collector (host:port)'), { target: { value: '10.0.0.5:2055' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(await within(card()).findByText('Commit to start')).toBeTruthy()
  })

  test('status: dropped records show only when there are some', async () => {
    vi.mocked(api.getFlowExportStatus).mockResolvedValue(status({ records_dropped: 1200 }))
    wrap(<FlowExportPage />)
    expect(await within(card()).findByText('Dropped (collector unreachable)')).toBeTruthy()
    expect(within(card()).getByText((1200).toLocaleString())).toBeTruthy()
  })

  test('status: no dropped line when nothing was dropped', async () => {
    vi.mocked(api.getFlowExportStatus).mockResolvedValue(status({ records_dropped: 0 }))
    wrap(<FlowExportPage />)
    await waitFor(() => expect(within(card()).getByTestId('flow-export-counters')).toBeTruthy())
    expect(within(card()).queryByText('Dropped (collector unreachable)')).toBeNull()
  })

  test('status: an endpoint that cannot be read is shown as an error', async () => {
    vi.mocked(api.getFlowExportStatus).mockRejectedValue(new Error('connection refused'))
    wrap(<FlowExportPage />)

    await waitFor(() => expect(within(card()).getByTestId('flow-export-error')).toBeTruthy())
    const line = within(card()).getByTestId('flow-export-error')
    expect(line).toHaveClass('text-red-600')
    expect(line).toHaveTextContent('Flow export status is not available. connection refused')
  })
})
