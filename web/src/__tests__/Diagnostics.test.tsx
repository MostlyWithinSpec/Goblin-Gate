// Diagnostics page (task 092): each tab sends the right body and renders the
// result; a 429 "busy" shows its own message.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/diagnostics', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/diagnostics')>()),
  diagPing: vi.fn(),
  diagTraceroute: vi.fn(),
  diagDNS: vi.fn(),
}))

vi.mock('../api/objects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/objects')>()),
  getCandidate: vi.fn(),
}))

import Diagnostics from '../pages/system/Diagnostics'
import { diagPing, diagTraceroute, diagDNS } from '../api/diagnostics'
import { getCandidate } from '../api/objects'
import { APIRequestError } from '../api/client'

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(getCandidate).mockReset().mockResolvedValue({
    interfaces: [{ name: 'eth1', device: 'eth1', mode: 'static', role: 'wan', enabled: true }],
    vlans: [{ name: 'vlan-10', parent: 'eth2', vlan_id: 10, mode: 'none', enabled: true }],
  })
  vi.mocked(diagPing).mockReset()
  vi.mocked(diagTraceroute).mockReset()
  vi.mocked(diagDNS).mockReset()
})

const pingResult = {
  target: '1.2.3.4', sent: 4, received: 4, loss_percent: 0,
  rtt_min_ms: 11.9, rtt_avg_ms: 12.3, rtt_max_ms: 13.1,
  lines: ['PING 1.2.3.4 (1.2.3.4): 56 data bytes', '64 bytes from 1.2.3.4: seq=0 ttl=64 time=12.3 ms'],
}

const traceResult = {
  target: 'example.com',
  hops: [
    { hop: 1, address: '10.0.0.1', rtt_ms: [1.1, 1.2] },
    { hop: 2, address: '*', rtt_ms: [] },
    { hop: 3, address: '93.184.216.34', rtt_ms: [12.0, 12.1, 12.2] },
  ],
}

const dnsResult = { name: 'example.com', type: 'A', server: '8.8.8.8', rcode: 'NOERROR', ms: 3.4, answers: ['93.184.216.34'] }

describe('Diagnostics', () => {
  it('ping tab sends the right body and renders the summary and raw lines', async () => {
    vi.mocked(diagPing).mockResolvedValue(pingResult)
    wrap(<Diagnostics />)
    fireEvent.change(screen.getByPlaceholderText('example.com or 1.2.3.4'), { target: { value: '1.2.3.4' } })
    // the interface select is populated from the candidate
    await waitFor(() => expect(screen.getByRole('option', { name: 'vlan-10' })).toBeInTheDocument())
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'eth1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ping' }))

    await waitFor(() => expect(vi.mocked(diagPing)).toHaveBeenCalledWith({ host: '1.2.3.4', count: 4, interface: 'eth1' }))
    expect(await screen.findByText('4 sent, 4 received, 0% loss, 12.3 ms average')).toBeInTheDocument()
    expect(screen.getByText(/PING 1\.2\.3\.4 \(1\.2\.3\.4\): 56 data bytes/)).toBeInTheDocument()
  })

  it('traceroute tab sends the right body and renders the hop table', async () => {
    vi.mocked(diagTraceroute).mockResolvedValue(traceResult)
    wrap(<Diagnostics />)
    fireEvent.click(screen.getByRole('button', { name: 'traceroute' }))
    const host = await screen.findByPlaceholderText('example.com or 1.2.3.4')
    fireEvent.change(host, { target: { value: 'example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Trace' }))

    await waitFor(() => expect(vi.mocked(diagTraceroute)).toHaveBeenCalledWith({ host: 'example.com', max_hops: 20 }))
    expect(await screen.findByText('10.0.0.1')).toBeInTheDocument()
    expect(screen.getByText('*')).toBeInTheDocument()
    expect(screen.getByText('93.184.216.34')).toBeInTheDocument()
    expect(screen.getByText(/12\.?0? ms\s+12\.1 ms\s+12\.2 ms/)).toBeInTheDocument()
  })

  it('dns tab sends the right body and renders rcode, time and answers', async () => {
    vi.mocked(diagDNS).mockResolvedValue(dnsResult)
    wrap(<Diagnostics />)
    fireEvent.click(screen.getByRole('button', { name: 'dns' }))
    const name = await screen.findByPlaceholderText('example.com')
    fireEvent.change(name, { target: { value: 'example.com' } })
    fireEvent.change(screen.getByDisplayValue('box'), { target: { value: '8.8.8.8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Lookup' }))

    await waitFor(() => expect(vi.mocked(diagDNS)).toHaveBeenCalledWith({ name: 'example.com', type: 'A', server: '8.8.8.8' }))
    expect(await screen.findByText('NOERROR · 3.4 ms · via 8.8.8.8')).toBeInTheDocument()
    expect(screen.getByText('93.184.216.34')).toBeInTheDocument()
  })

  it('shows the busy message on a 429', async () => {
    vi.mocked(diagPing).mockRejectedValue(
      new APIRequestError('another diagnostic is running; try again in a moment', 429, 'busy', null),
    )
    wrap(<Diagnostics />)
    fireEvent.change(screen.getByPlaceholderText('example.com or 1.2.3.4'), { target: { value: '1.2.3.4' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ping' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Another diagnostic is running; try again in a moment.')
  })

  it('shows the server message on other errors', async () => {
    vi.mocked(diagTraceroute).mockRejectedValue(
      new APIRequestError('unknown host "nope.example"', 400, 'diag_failed', null),
    )
    wrap(<Diagnostics />)
    fireEvent.click(screen.getByRole('button', { name: 'traceroute' }))
    const host = await screen.findByPlaceholderText('example.com or 1.2.3.4')
    fireEvent.change(host, { target: { value: 'nope.example' } })
    fireEvent.click(screen.getByRole('button', { name: 'Trace' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('unknown host "nope.example"')
  })
})
