import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

vi.mock('../api/setup', () => ({ getDevices: vi.fn(), submitSetup: vi.fn(), getSetupState: vi.fn() }))
import * as api from '../api/setup'
import SetupWizard from '../pages/setup/SetupWizard'
import SetupGate from '../components/SetupGate'

beforeEach(() => {
  vi.mocked(api.getDevices).mockResolvedValue([
    { name: 'enp1s0', mac: 'aa:bb:cc:00:00:01', up: true },
    { name: 'enp2s0', mac: 'aa:bb:cc:00:00:02', up: false },
  ])
  vi.mocked(api.submitSetup).mockReset().mockResolvedValue({ ok: true, revision: 1 })
})

const next = () => fireEvent.click(screen.getByRole('button', { name: /^(Start|Next)$/ }))
const heading = (name: RegExp) => screen.findByRole('heading', { name })

async function toPassword() {
  render(<SetupWizard />)
  await waitFor(() => expect(api.getDevices).toHaveBeenCalled())
  next()
  await heading(/Admin password/)
}

async function fillPassword() {
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'correct horse battery' } })
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'correct horse battery' } })
  next()
}

describe('SetupWizard', () => {
  test('blocks a weak or mismatched password', async () => {
    await toPassword()
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'short' } })
    expect(screen.getByText('Too weak')).toBeInTheDocument()
    next()
    expect(await screen.findByText('At least 10 characters')).toBeInTheDocument()
    expect(screen.getByText('Passwords do not match')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /Admin password/ })).toBeInTheDocument()
  })

  test('static WAN needs an address and gateway', async () => {
    await toPassword()
    await fillPassword()
    await heading(/Internet/)
    expect(screen.getByLabelText('WAN device')).toHaveValue('enp1s0')
    fireEvent.click(screen.getByLabelText(/Static address/))
    next()
    expect(await screen.findByText(/IPv4 address with prefix/)).toBeInTheDocument()
    expect(screen.getByText(/ISP gateway/)).toBeInTheDocument()
  })

  test('LAN step suggests a DHCP range and rejects the WAN device', async () => {
    await toPassword()
    await fillPassword()
    await heading(/Internet/)
    next()
    await heading(/Home network/)
    fireEvent.change(screen.getByLabelText('LAN address'), { target: { value: '10.20.0.1/24' } })
    expect(screen.getByLabelText('Range start')).toHaveValue('10.20.0.100')
    expect(screen.getByLabelText('Range end')).toHaveValue('10.20.0.199')
    fireEvent.change(screen.getByLabelText('LAN device'), { target: { value: 'enp1s0' } })
    next()
    expect(await screen.findByText('LAN and WAN must use different devices')).toBeInTheDocument()
  })

  test('submits the answers and shows server errors on failure', async () => {
    vi.mocked(api.submitSetup).mockResolvedValueOnce({ ok: false, message: 'the setup answers have errors', errors: [{ path: 'lan.dhcp_end', message: 'range end is before range start' }] })
    await toPassword()
    await fillPassword()
    await heading(/Internet/)
    next()
    await heading(/Home network/)
    next()
    expect(await screen.findByText('Security level')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/Strict/))
    next()
    await heading(/Review/)
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(await screen.findByText(/range end is before range start/)).toBeInTheDocument()
    expect(api.submitSetup).toHaveBeenCalledWith({
      admin_password: 'correct horse battery',
      wan: { device: 'enp1s0', mode: 'dhcp' },
      lan: { device: 'enp2s0', address: '192.168.1.1/24', dhcp_start: '192.168.1.100', dhcp_end: '192.168.1.199' },
      security_level: 'strict',
    })
    fireEvent.click(screen.getByRole('button', { name: 'LAN' }))
    await heading(/Home network/)
  })

  test('shows the done page after a successful apply', async () => {
    await toPassword()
    await fillPassword()
    await heading(/Internet/)
    next()
    await heading(/Home network/)
    next()
    await screen.findByText('Security level')
    next()
    await heading(/Review/)
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(await heading(/All set/)).toBeInTheDocument()
  })
})

describe('SetupGate', () => {
  const app = () =>
    render(
      <MemoryRouter initialEntries={['/policy']}>
        <SetupGate />
        <Routes>
          <Route path="/policy" element={<p>policy page</p>} />
          <Route path="/setup" element={<p>setup page</p>} />
        </Routes>
      </MemoryRouter>
    )

  test('redirects to /setup when setup is needed', async () => {
    vi.mocked(api.getSetupState).mockResolvedValue({ needed: true })
    app()
    expect(await screen.findByText('setup page')).toBeInTheDocument()
  })

  test('stays put once setup is done', async () => {
    vi.mocked(api.getSetupState).mockResolvedValue({ needed: false })
    app()
    await waitFor(() => expect(api.getSetupState).toHaveBeenCalled())
    expect(screen.getByText('policy page')).toBeInTheDocument()
  })
})
