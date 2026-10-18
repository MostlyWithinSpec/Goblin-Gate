import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getDDNSStatus: vi.fn(),
    updateDDNSNow: vi.fn(),
  }
})

import * as api from '../api/objects'
import DynamicDNS from '../pages/network/DynamicDNS'

const cfg: Candidate = {
  interfaces: [
    { name: 'wan1', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true },
  ],
  vlans: [{ name: 'guest', parent: 'lan', vlan_id: 20, mode: 'static', address: '192.168.20.1/24', enabled: true }],
}

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
  vi.mocked(api.getDDNSStatus).mockResolvedValue([])
  vi.mocked(api.updateDDNSNow).mockResolvedValue({ name: 'home', hostname: 'vpn.example.com', ip: '1.2.3.4' })
})

describe('DynamicDNS page', () => {
  it('adds a Cloudflare entry with the right section body', async () => {
    wrap(<DynamicDNS />)
    await screen.findByRole('button', { name: /New entry/i })
    fireEvent.click(screen.getByRole('button', { name: /New entry/i }))

    // Fill in the form
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'home' } })
    fireEvent.change(screen.getByLabelText('Hostname'), { target: { value: 'vpn.example.com' } })
    fireEvent.change(screen.getByLabelText('API token'), { target: { value: 'cf-token-123' } })
    fireEvent.change(screen.getByLabelText('Zone ID'), { target: { value: 'abc-zone-id' } })

    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('ddns')
    const arr = value as api.DDNS[]
    expect(arr).toHaveLength(1)
    expect(arr[0]).toMatchObject({
      name: 'home',
      enabled: true,
      provider: 'cloudflare',
      hostname: 'vpn.example.com',
      token: 'cf-token-123',
      zone_id: 'abc-zone-id',
    })
  })

  it('keeps the stored token when editing without a new one', async () => {
    const cfgWithDDNS: Candidate = {
      ...cfg,
      ddns: [{ name: 'home', enabled: true, provider: 'cloudflare', hostname: 'vpn.example.com', zone_id: 'abc-zone-id' }],
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfgWithDDNS)

    wrap(<DynamicDNS />)
    // Click the entry name to edit
    await screen.findByRole('button', { name: 'home' })
    fireEvent.click(screen.getByRole('button', { name: 'home' }))

    // Token field should show placeholder "unchanged" and be empty
    const tokenInput = screen.getByLabelText('API token') as HTMLInputElement
    expect(tokenInput.value).toBe('')
    expect(tokenInput.placeholder).toBe('unchanged')

    // Change only the hostname
    fireEvent.change(screen.getByLabelText('Hostname'), { target: { value: 'new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    const arr = value as api.DDNS[]
    expect(arr[0].hostname).toBe('new.example.com')
    // token must be absent (omitEmpty removes it) so the server keeps the stored one
    expect(arr[0].token).toBeUndefined()
  })

  it('shows status column and Update now button', async () => {
    const cfgWithDDNS: Candidate = {
      ...cfg,
      ddns: [{ name: 'home', enabled: true, provider: 'cloudflare', hostname: 'vpn.example.com' }],
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfgWithDDNS)
    const now = new Date()
    vi.mocked(api.getDDNSStatus).mockResolvedValue([
      { name: 'home', hostname: 'vpn.example.com', ip: '9.8.7.6', last_update: now.toISOString(), last_error: 'timeout' },
    ])

    wrap(<DynamicDNS />)
    // Status column shows IP and error
    await screen.findByText('9.8.7.6')
    expect(screen.getByText('timeout')).toHaveClass('text-red-600')

    // Update now button exists and calls the API
    const btn = screen.getByRole('button', { name: /Update now/i })
    fireEvent.click(btn)
    await waitFor(() => expect(api.updateDDNSNow).toHaveBeenCalledWith('home'))
  })
})
