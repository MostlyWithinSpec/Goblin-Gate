// Throughput test and network card tabs of System → Diagnostics.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/diagnostics', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/diagnostics')>()),
  getThroughput: vi.fn(),
  startThroughput: vi.fn(),
  stopThroughput: vi.fn(),
  getNICs: vi.fn(),
}))

vi.mock('../api/objects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/objects')>()),
  getCandidate: vi.fn(),
}))

import Diagnostics from '../pages/system/Diagnostics'
import { mbit } from '../pages/system/Throughput'
import { getNICs, getThroughput, startThroughput, stopThroughput } from '../api/diagnostics'
import type { ThroughputState } from '../api/diagnostics'
import { getCandidate } from '../api/objects'
import { APIRequestError } from '../api/client'

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

const idle: ThroughputState = { running: false, port: 5201, results: [] }
const running = (): ThroughputState => ({
  running: true, interface: 'lan', device: 'eth1', address: '192.168.1.1', port: 5201,
  started: new Date().toISOString(), expires: new Date(Date.now() + 5 * 60_000).toISOString(), results: [],
})

beforeEach(() => {
  vi.mocked(getCandidate).mockReset().mockResolvedValue({
    interfaces: [
      { name: 'wan1', device: 'eth0', mode: 'dhcp', role: 'wan', enabled: true },
      { name: 'lan', device: 'eth1', mode: 'static', role: 'lan', address: '192.168.1.1/24', enabled: true },
      { name: 'off', device: 'eth2', mode: 'static', role: 'lan', address: '10.9.0.1/24', enabled: false },
    ],
    vlans: [
      { name: 'iot', parent: 'lan', vlan_id: 30, mode: 'static', address: '192.168.30.1/24', enabled: true },
      { name: 'wanvlan', parent: 'wan1', vlan_id: 7, mode: 'static', address: '100.64.0.2/24', enabled: true },
    ],
  })
  vi.mocked(getThroughput).mockReset().mockResolvedValue(idle)
  vi.mocked(startThroughput).mockReset()
  vi.mocked(stopThroughput).mockReset()
  vi.mocked(getNICs).mockReset()
})

const openTab = async (name: string) => {
  wrap(<Diagnostics />)
  fireEvent.click(screen.getByRole('tab', { name }))
}

describe('Throughput test', () => {
  it('offers only enabled LAN-side networks with a static IPv4 address', async () => {
    await openTab('Throughput test')
    const select = await screen.findByLabelText('Network')
    await waitFor(() => expect([...select.querySelectorAll('option')].map((o) => o.value)).toEqual(['iot', 'lan']))
  })

  it('starts, shows the client commands, and stops', async () => {
    vi.mocked(startThroughput).mockResolvedValue(running())
    vi.mocked(stopThroughput).mockResolvedValue({ ...idle, interface: 'lan' })
    await openTab('Throughput test')
    const select = await screen.findByLabelText('Network')
    await waitFor(() => expect(select).toHaveValue('iot'))
    fireEvent.change(select, { target: { value: 'lan' } })
    fireEvent.change(screen.getByLabelText('Minutes'), { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: 'Start' }))
    await waitFor(() => expect(startThroughput).toHaveBeenCalledWith({ interface: 'lan', minutes: 3 }))
    expect(await screen.findByText(/Listening on lan \(192\.168\.1\.1\), [45]:\d\d left/)).toBeInTheDocument()
    expect(screen.getByText('iperf3 -c 192.168.1.1')).toBeInTheDocument()
    expect(screen.getByText('iperf3 -c 192.168.1.1 -R')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(stopThroughput).toHaveBeenCalled())
    expect(await screen.findByRole('button', { name: 'Start' })).toBeInTheDocument()
  })

  it('shows results newest first in Mbit/s', async () => {
    vi.mocked(getThroughput).mockResolvedValue({
      ...idle,
      results: [
        { time: '2026-11-06T10:00:00Z', client: '192.168.1.50', protocol: 'tcp', direction: 'upload', streams: 1, seconds: 10, bits_per_second: 941_234_567 },
        { time: '2026-11-06T10:01:00Z', client: '192.168.1.50', protocol: 'tcp', direction: 'download', streams: 4, seconds: 10, bits_per_second: 912_000_000, retransmits: 3 },
      ],
    })
    await openTab('Throughput test')
    const rows = await screen.findAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(rows[1]).toHaveTextContent('Download')
    expect(rows[1]).toHaveTextContent('912.0 Mbit/s')
    expect(rows[1]).toHaveTextContent('3 retransmits')
    expect(rows[2]).toHaveTextContent('941.2 Mbit/s')
  })

  it('names the conflict when a test is already running', async () => {
    vi.mocked(startThroughput).mockRejectedValue(new APIRequestError('a throughput test is already running', 409, 'throughput_running', null))
    await openTab('Throughput test')
    await waitFor(() => expect(screen.getByLabelText('Network')).toHaveValue('iot'))
    fireEvent.click(screen.getByRole('button', { name: 'Start' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('A throughput test is already running.')
  })

  it('picks up a test that is already running and polls it', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      vi.mocked(getThroughput).mockResolvedValue(running())
      await openTab('Throughput test')
      expect(await screen.findByRole('button', { name: 'Stop' })).toBeInTheDocument()
      const calls = vi.mocked(getThroughput).mock.calls.length
      await vi.advanceTimersByTimeAsync(4100)
      expect(vi.mocked(getThroughput).mock.calls.length).toBeGreaterThanOrEqual(calls + 2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('formats Mbit/s with one decimal', () => {
    expect(mbit(941_234_567)).toBe('941.2')
    expect(mbit(1_000_000)).toBe('1.0')
  })
})

describe('Network cards', () => {
  it('shows driver, link, queues, offloads and hints', async () => {
    vi.mocked(getNICs).mockResolvedValue({
      interfaces: [{
        name: 'lan', role: 'lan', enabled: true, device: 'eth1', driver: 'r8169', link: true, speed_mbps: 100, duplex: 'full',
        mtu: 1500, rx_queues: 1, tx_queues: 1, supported_mbps: [10, 100, 1000],
        offloads: { 'generic-receive-offload': { on: false }, 'tcp-segmentation-offload': { on: true, fixed: true } },
        hints: [{ code: 'slow_link', message: 'The link runs at 100 Mbit/s although the card can do 1000 Mbit/s: check the cable.' }],
      }],
    })
    await openTab('Network cards')
    expect(await screen.findByText('r8169')).toBeInTheDocument()
    expect(screen.getByText('100 Mbit/s full')).toBeInTheDocument()
    expect(screen.getByText('1 receive, 1 send')).toBeInTheDocument()
    expect(screen.getByText('GRO off')).toBeInTheDocument()
    expect(screen.getByText('TSO on')).toBeInTheDocument()
    expect(screen.getByText('The link runs at 100 Mbit/s although the card can do 1000 Mbit/s: check the cable.')).toBeInTheDocument()
  })

  it('says when a card has no link', async () => {
    vi.mocked(getNICs).mockResolvedValue({
      interfaces: [{ name: 'wan1', role: 'wan', enabled: true, device: 'eth0', link: false, speed_mbps: 0, rx_queues: 4, tx_queues: 4, hints: [{ code: 'no_link', message: '' }] }],
    })
    await openTab('Network cards')
    expect(await screen.findByText('no link')).toBeInTheDocument()
    expect(screen.getByText('No link: check the cable and the device on the other end.')).toBeInTheDocument()
  })
})
