// Network → Reverse Proxy: the settings card saves the whole reverse_proxy
// object, the hosts table edits reverse_proxy.hosts, and the status table shows
// what the box has published. No network: the API module is mocked.
import React from 'react'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate, ProxyStatus } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getProxyStatus: vi.fn(),
  }
})

import * as api from '../api/objects'
import ReverseProxy from '../pages/network/ReverseProxy'

// The page has its own settings form, so the host drawer is queried on its own.
const drawer = () => screen.getByRole('dialog', { name: /^Edit |^Publish/ })

const base: Candidate = {
  addresses: [
    { name: 'office', type: 'subnet', value: '192.168.10.0/24' },
    { name: 'home-pc', type: 'host', value: '192.168.10.20' },
    { name: 'example-range', type: 'range', value: '192.168.10.50-192.168.10.60' },
  ],
}

const status: ProxyStatus = {
  enabled: true,
  state: 'active',
  hosts: [
    {
      name: 'jellyfin',
      enabled: true,
      upstream: 'http://10.0.0.2:8096',
      domains: [
        { domain: 'media.example.com', certificate: { issuer: "Let's Encrypt R3", not_before: '2026-10-01T00:00:00Z', not_after: '2026-12-30T00:00:00Z', obtained: '2026-10-01T00:05:00Z' } },
        { domain: 'staging.example.com', certificate: { issuer: "Let's Encrypt R3", not_before: '2026-10-01T00:00:00Z', not_after: '2026-12-30T00:00:00Z', obtained: '2026-10-01T00:05:00Z', staging: true } },
        { domain: 'broken.example.com', error: { time: '2026-11-05T10:00:00Z', message: 'acme: could not reach HTTP-01 challenge' } },
        { domain: 'new.example.com' },
      ],
    },
  ],
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, refetchInterval: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(base))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getProxyStatus).mockReset().mockResolvedValue(structuredClone(status))
})

describe('ReverseProxy settings', () => {
  it('saves the whole reverse_proxy object with the email and staging', async () => {
    wrap(<ReverseProxy />)
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    fireEvent.click(within(form).getByRole('checkbox', { name: /enable the reverse proxy/i }))
    fireEvent.change(within(form).getByLabelText('Email'), { target: { value: 'admin@example.com' } })
    fireEvent.click(within(form).getByRole('checkbox', { name: /staging certificates/i }))
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('reverse_proxy')
    expect(value).toEqual({ enabled: true, email: 'admin@example.com', staging: true, hosts: [] })
    expect(await screen.findByRole('status')).toHaveTextContent(/commit bar/i)
  })

  it('keeps lan_access on unless it is switched off', async () => {
    wrap(<ReverseProxy />)
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    const lan = within(form).getByRole('checkbox', { name: /lan access/i })
    expect(lan).toBeChecked()
    fireEvent.click(lan)
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()[1]).toEqual({ enabled: false, lan_access: false, hosts: [] })
  })

  it('rejects a malformed email before saving', async () => {
    wrap(<ReverseProxy />)
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    fireEvent.change(within(form).getByLabelText('Email'), { target: { value: 'nope' } })
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/valid email/i)
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('shows a server error for the email next to the email field', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'reverse_proxy.email', message: 'invalid email address "bad"' }],
    })
    wrap(<ReverseProxy />)
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    const emailField = within(form).getByLabelText('Email').closest('label') as HTMLElement
    fireEvent.change(within(form).getByLabelText('Email'), { target: { value: 'bad@example.com' } })
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('invalid email address "bad"')
    // the message sits inside the Email label, not in a list of paths
    expect(emailField).toContainElement(alert)
  })

  it('shows an enabled-state error next to the enable checkbox', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'reverse_proxy.enabled', message: 'the reverse proxy needs an enabled WAN interface' }],
    })
    wrap(<ReverseProxy />)
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    fireEvent.click(within(form).getByRole('checkbox', { name: /enable the reverse proxy/i }))
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/enabled WAN interface/)
    expect(form).toContainElement(alert)
  })
})

describe('ReverseProxy hosts', () => {
  it('adds a host with its domains, upstream and allowed sources', async () => {
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: /publish a service/i }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'jellyfin' } })
    fireEvent.change(screen.getByLabelText('Domains'), { target: { value: 'media.example.com' } })
    fireEvent.change(screen.getByLabelText('Upstream'), { target: { value: 'http://10.0.0.2:8096' } })
    fireEvent.change(screen.getByLabelText('Add allowed source'), { target: { value: 'office' } })
    fireEvent.change(screen.getByLabelText('Or type an address or CIDR'), { target: { value: '203.0.113.7' } })

    fireEvent.click(within(drawer()).getByRole('button', { name: /^Save$/i }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('reverse_proxy.hosts')
    expect(value).toEqual([
      {
        name: 'jellyfin',
        enabled: true,
        domains: ['media.example.com'],
        upstream: 'http://10.0.0.2:8096',
        allow: ['office', '203.0.113.7'],
      },
    ])
  })

  it('only offers host and subnet address objects as allowed sources', async () => {
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: /publish a service/i }))
    const pick = screen.getByLabelText('Add allowed source') as HTMLSelectElement
    expect(within(pick).getByRole('option', { name: /office/ })).toBeInTheDocument()
    expect(within(pick).getByRole('option', { name: /home-pc/ })).toBeInTheDocument()
    expect(within(pick).queryByRole('option', { name: /example-range/ })).toBeNull()
  })

  it('edits an existing host in place', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [{ name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8096' }],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: 'jellyfin' }))
    fireEvent.change(screen.getByLabelText('Upstream'), { target: { value: 'http://10.0.0.2:8097' } })
    fireEvent.click(within(drawer()).getByRole('button', { name: /^Save$/i }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()[1]).toEqual([
      { name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8097' },
    ])
  })

  it('deletes a host', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [
          { name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8096' },
          { name: 'NAS', enabled: true, domains: ['files.example.com'], upstream: 'https://nas.local' },
        ],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete jellyfin' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()[1]).toEqual([{ name: 'NAS', enabled: true, domains: ['files.example.com'], upstream: 'https://nas.local' }])
    confirm.mockRestore()
  })

  it('shows the skip certificate check box only for an https upstream', async () => {
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: /publish a service/i }))
    const upstream = screen.getByLabelText('Upstream')
    expect(screen.queryByRole('checkbox', { name: /skip certificate check/i })).toBeNull()
    fireEvent.change(upstream, { target: { value: 'https://nas.local' } })
    expect(await screen.findByRole('checkbox', { name: /skip certificate check/i })).toBeInTheDocument()
    fireEvent.change(upstream, { target: { value: 'http://10.0.0.2:8096' } })
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: /skip certificate check/i })).toBeNull())
  })

  it('drops the insecure flag when the upstream goes back to http', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [
          { name: 'NAS', enabled: true, domains: ['files.example.com'], upstream: 'https://nas.local', insecure_skip_verify: true },
        ],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: 'NAS' }))
    expect(screen.getByRole('checkbox', { name: /skip certificate check/i })).toBeChecked()
    fireEvent.change(screen.getByLabelText('Upstream'), { target: { value: 'http://10.0.0.2:80' } })
    fireEvent.click(within(drawer()).getByRole('button', { name: /^Save$/i }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect((lastPatch()[1] as api.ProxyHost[])[0].insecure_skip_verify).toBeUndefined()
  })

  it('shows a host validation error from the server on its field', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [{ name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8096' }],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'reverse_proxy.hosts[0].upstream', message: 'upstream is the box own address on lan' }],
    })
    wrap(<ReverseProxy />)
    // the settings save carries the host messages: they sit under the card
    // that saved them, not in the table's own error list
    const form = await screen.findByRole('form', { name: /reverse proxy settings/i })
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/i }))
    const list = await screen.findByRole('host-errors')
    expect(list).toHaveTextContent('upstream is the box own address on lan')
  })

  it('rejects a domain that is not a public name', async () => {
    wrap(<ReverseProxy />)
    fireEvent.click(await screen.findByRole('button', { name: /publish a service/i }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'a' } })
    fireEvent.change(screen.getByLabelText('Domains'), { target: { value: '*.example.com' } })
    fireEvent.change(screen.getByLabelText('Upstream'), { target: { value: 'http://10.0.0.2:80' } })
    fireEvent.click(within(drawer()).getByRole('button', { name: /^Save$/i }))
    expect(await within(drawer()).findByRole('alert')).toHaveTextContent(/no wildcards/i)
    expect(api.patchSection).not.toHaveBeenCalled()
  })
})

describe('ReverseProxy status', () => {
  it('shows the certificate, the staging badge, the error and the waiting state', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [{ name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8096' }],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    wrap(<ReverseProxy />)
    // the table lists every domain of the status, so the issuer appears once
    // per row; a row's certificate sits next to its own domain
    const certs = await screen.findByRole('table', { name: /certificates/i })
    const media = within(certs).getByText('media.example.com').closest('tr') as HTMLTableRowElement
    expect(within(media).getByText(/Let's Encrypt R3/)).toBeInTheDocument()
    expect(within(media).queryByText('Staging')).toBeNull()
    expect(within(certs).getByText('media.example.com')).toBeInTheDocument()
    expect(within(certs).getAllByText('Staging')).toHaveLength(1)
    const err = within(certs).getByText(/could not reach HTTP-01 challenge/)
    expect(err.className).toContain('text-red-600')
    expect(within(certs).getByText('Waiting for a certificate')).toBeInTheDocument()
  })

  it('asks for a commit for a host that is not published yet', async () => {
    const cfg: Candidate = {
      ...base,
      reverse_proxy: {
        enabled: true,
        hosts: [
          { name: 'jellyfin', enabled: true, domains: ['media.example.com'], upstream: 'http://10.0.0.2:8096' },
          { name: 'grafana', enabled: true, domains: ['stats.example.com'], upstream: 'http://10.0.0.3:3000' },
        ],
      },
    }
    vi.mocked(api.getCandidate).mockResolvedValue(cfg)
    wrap(<ReverseProxy />)
    expect(await screen.findByText('Commit to publish')).toBeInTheDocument()
  })

  it('says when the proxy is not running', async () => {
    vi.mocked(api.getProxyStatus).mockResolvedValue({ enabled: false, state: 'inactive', hosts: [] })
    wrap(<ReverseProxy />)
    expect(await screen.findByText('inactive')).toBeInTheDocument()
    expect(screen.getByText(/not running/i)).toBeInTheDocument()
  })
})
