import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/system', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/system')>()
  return { ...real, getUpdates: vi.fn(), applyUpdate: vi.fn() }
})

import * as api from '../api/objects'
import * as sys from '../api/system'
import Updates from '../pages/system/Updates'

const cfg: Candidate = { system: { hostname: 'gw1', timezone: 'UTC', update_channel: 'stable' } }

const baseStatus = { installed: '0.1.0~alpha.1', checked: '2026-10-15T12:00:00Z' }

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(sys.getUpdates).mockReset().mockResolvedValue({ ...baseStatus })
  vi.mocked(sys.applyUpdate).mockReset().mockResolvedValue({ status: 'started' })
})

describe('Updates page', () => {
  test('shows installed and available versions with release notes link', async () => {
    vi.mocked(sys.getUpdates).mockResolvedValue({
      ...baseStatus,
      available: '0.1.0~alpha.2',
      release_url: 'https://example.com/notes',
      os_updates: 3,
    })
    wrap(<Updates />)
    // "~" is displayed as "-"
    expect(await screen.findByText('0.1.0-alpha.1')).toBeInTheDocument()
    expect(screen.getByText('0.1.0-alpha.2')).toBeInTheDocument()
    const link = screen.getByRole('link', { name: /Release notes/ })
    expect(link).toHaveAttribute('href', 'https://example.com/notes')
    expect(screen.getByText(/3 Debian package updates pending/)).toBeInTheDocument()
  })

  test('says up to date when no version is available', async () => {
    wrap(<Updates />)
    expect(await screen.findByText('Up to date')).toBeInTheDocument()
    // The section heading "Update now" is always present; assert the button is gone.
    expect(screen.queryByRole('button', { name: 'Update now' })).not.toBeInTheDocument()
  })

  test('PATCHes the system section with the new channel on save', async () => {
    wrap(<Updates />)
    await screen.findByText('0.1.0-alpha.1')
    fireEvent.change(screen.getByLabelText('Update channel'), { target: { value: 'beta' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save channel' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const calls = vi.mocked(api.patchSection).mock.calls
    expect(calls[0][0]).toBe('system')
    expect(calls[0][1]).toMatchObject({ update_channel: 'beta' })
  })

  test('confirm dialog gates the update and applyUpdate is called on confirm', async () => {
    vi.mocked(sys.getUpdates).mockResolvedValue({ ...baseStatus, available: '0.1.0~alpha.2' })
    wrap(<Updates />)
    await screen.findByText('0.1.0-alpha.2')

    // Click "Update now" — the dialog appears first, no API call yet.
    fireEvent.click(screen.getByRole('button', { name: 'Update now' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(sys.applyUpdate).not.toHaveBeenCalled()

    // Cancel closes the dialog without applying.
    const cancelDialog = screen.getByRole('dialog')
    fireEvent.click(within(cancelDialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(sys.applyUpdate).not.toHaveBeenCalled()

    // Confirm applies with the available version.
    fireEvent.click(screen.getByRole('button', { name: 'Update now' }))
    const confirmDialog = await screen.findByRole('dialog')
    fireEvent.click(within(confirmDialog).getByRole('button', { name: 'Update now' }))
    await waitFor(() => expect(sys.applyUpdate).toHaveBeenCalled())
    expect(sys.applyUpdate).toHaveBeenCalledWith({ version: '0.1.0~alpha.2', include_os: false })
  })

  test('shows the rolled-back result with its reason', async () => {
    vi.mocked(sys.getUpdates).mockResolvedValue({
      ...baseStatus,
      available: '',
      last: { state: 'rolled_back', from: '0.1.0~alpha.1', to: '0.1.0~alpha.2', message: 'new version failed health check' },
    })
    wrap(<Updates />)
    expect(await screen.findByText('Update rolled back')).toBeInTheDocument()
    expect(screen.getByText(/0.1.0-alpha.1 → 0.1.0-alpha.2/)).toBeInTheDocument()
    expect(screen.getByText('new version failed health check')).toBeInTheDocument()
  })
})
