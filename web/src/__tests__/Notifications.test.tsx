import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

// Mock the API module: no network. The pure helpers (validateChannel) stay real.
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    testNotification: vi.fn(),
  }
})
import * as api from '../api/objects'
import Notifications from '../pages/system/Notifications'

const cfg: Candidate = {
  notifications: {
    channels: [
      { name: 'hook', type: 'webhook', enabled: true, url: 'https://example.org/hook' },
      { name: 'mail', type: 'email', enabled: true, smtp_host: 'smtp.example.com', smtp_port: 587, from: 'alerts@example.com', to: ['ops@example.com'] },
    ],
    rules: [{ event: 'commit', channels: ['hook'] }],
  },
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}
const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.testNotification).mockReset().mockResolvedValue({ status: 'sent' })
})

describe('Notifications channels', () => {
  test('renders the list from the candidate', async () => {
    wrap(<Notifications />)
    expect(await screen.findByRole('button', { name: 'hook' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'mail' })).toBeInTheDocument()
  })

  test('webhook with a non-http URL is rejected (no PATCH)', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /New channel/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'x' } })
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'ftp://nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/http:\/\/ or https:\/\//)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('a valid webhook PATCHes the whole channels list', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /New channel/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'discord' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'discord' } })
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'https://discord.com/api/webhooks/1/2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('notifications.channels')
    expect((list as api.NotifyChannel[])).toContainEqual({ name: 'discord', type: 'discord', enabled: true, url: 'https://discord.com/api/webhooks/1/2' })
  })

  test('email requires a valid sender and at least one recipient', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /New channel/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'm2' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'email' } })
    // leave smtp_host empty, from invalid, to empty
    fireEvent.change(screen.getByLabelText('From'), { target: { value: 'not-an-address' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/An SMTP server is required/)).toBeInTheDocument()
    expect(screen.getByText(/valid sender address/)).toBeInTheDocument()
    expect(screen.getByText(/At least one recipient is required/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('email port out of range is rejected', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /New channel/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'm3' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'email' } })
    fireEvent.change(screen.getByLabelText('SMTP server'), { target: { value: 'smtp.example.com' } })
    fireEvent.change(screen.getByLabelText('SMTP port'), { target: { value: '70000' } })
    fireEvent.change(screen.getByLabelText('From'), { target: { value: 'a@b.com' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: 'c@d.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/0-65535/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('telegram requires a bot token and chat id', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /New channel/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'tg' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'telegram' } })
    fireEvent.change(screen.getByLabelText('Bot token'), { target: { value: 'bad/token' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Telegram bot token/)).toBeInTheDocument()
    expect(screen.getByText(/Telegram chat id/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('Send test reports the result inline', async () => {
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /Send test to hook/ }))
    expect(await screen.findByTestId('test-hook')).toHaveTextContent('sent')
    expect(api.testNotification).toHaveBeenCalledWith('hook')
  })

  test('Send test shows the server reason on failure', async () => {
    vi.mocked(api.testNotification).mockRejectedValue(new Error('the running config has no channel nope (commit it first)'))
    wrap(<Notifications />)
    await screen.findByRole('button', { name: 'hook' })
    fireEvent.click(screen.getByRole('button', { name: /Send test to hook/ }))
    expect(await screen.findByTestId('test-hook')).toHaveTextContent(/no channel/)
  })
})

describe('Notifications rules', () => {
  test('toggling a rule PATCHes notifications.rules', async () => {
    wrap(<Notifications />)
    // wait for the rules table to load (channel headers present)
    await screen.findByRole('checkbox', { name: 'commit → hook' })
    // commit already has hook; add mail too
    fireEvent.click(screen.getByRole('checkbox', { name: 'commit → mail' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('notifications.rules')
    expect(list as api.NotifyRule[]).toContainEqual({ event: 'commit', channels: ['hook', 'mail'] })

    // uncheck hook again → commit keeps only mail
    fireEvent.click(screen.getByRole('checkbox', { name: 'commit → hook' }))
    await waitFor(() =>
      expect((lastPatch()[1] as api.NotifyRule[]).find((r) => r.event === 'commit')).toEqual({ event: 'commit', channels: ['mail'] })
    )
  })

  test('unchecking the last channel drops the rule entirely', async () => {
    wrap(<Notifications />)
    await screen.findByRole('checkbox', { name: 'commit → hook' })
    fireEvent.click(screen.getByRole('checkbox', { name: 'commit → hook' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const list = lastPatch()[1] as api.NotifyRule[]
    expect(list.find((r) => r.event === 'commit')).toBeUndefined()
  })
})
