import { beforeEach, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/snapshots', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/snapshots')>()
  return {
    ...real,
    listSnapshots: vi.fn(),
    saveSnapshot: vi.fn(),
    getSnapshot: vi.fn(),
    restoreSnapshot: vi.fn(),
    deleteSnapshot: vi.fn(),
  }
})

vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getConfigDiff: vi.fn() }
})

import * as snapshotsApi from '../api/snapshots'
import * as configApi from '../api/config'
import { Snapshots } from '../pages/system/Snapshots'

const renderPage = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Snapshots />
    </QueryClientProvider>,
  )

beforeEach(() => {
  vi.mocked(snapshotsApi.listSnapshots).mockReset().mockResolvedValue([
    { name: 'pre-upgrade', comment: 'before the upgrade', created: '2026-10-16T14:05:00Z', created_by: 'admin', revision: 3 },
    { name: 'baseline', created: '2026-10-15T09:30:00Z', created_by: 'web', revision: 2 },
  ])
  vi.mocked(snapshotsApi.saveSnapshot).mockReset().mockResolvedValue({ name: 'x', created: '2026-10-17T00:00:00Z', created_by: 'admin', revision: 4 })
  vi.mocked(snapshotsApi.restoreSnapshot).mockReset().mockResolvedValue({ status: 'ok', changes: 5 })
  vi.mocked(snapshotsApi.deleteSnapshot).mockReset().mockResolvedValue(undefined)
  vi.mocked(configApi.getConfigDiff).mockReset().mockResolvedValue({ count: 5, changes: [] })
})

test('lists snapshots with their details', async () => {
  renderPage()
  const rows = await screen.findAllByRole('row')
  // header + 2 data rows
  expect(rows).toHaveLength(3)
  expect(within(rows[1]).getByText('pre-upgrade')).toBeInTheDocument()
  expect(within(rows[1]).getByText('before the upgrade')).toBeInTheDocument()
  expect(screen.queryByText(/Invalid Date/)).not.toBeInTheDocument()
})

test('save sends the right body', async () => {
  renderPage()
  await screen.findAllByRole('row')
  fireEvent.change(screen.getByPlaceholderText('pre-upgrade'), { target: { value: 'pre-upgrade' } })
  fireEvent.change(screen.getByPlaceholderText('before the upgrade'), { target: { value: 'just in case' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Snapshot' }))
  await waitFor(() => expect(snapshotsApi.saveSnapshot).toHaveBeenCalledWith('pre-upgrade', 'just in case'))
})

test('restore calls the API after confirm and refreshes the diff', async () => {
  renderPage()
  const rows = await screen.findAllByRole('row')
  fireEvent.click(within(rows[1]).getByRole('button', { name: 'Restore' }))
  // the confirm dialog names the snapshot
  expect(screen.getByText(/Load snapshot “pre-upgrade” as the candidate\?/)).toBeInTheDocument()
  const restoreButtons = screen.getAllByRole('button', { name: 'Restore' })
  fireEvent.click(restoreButtons[restoreButtons.length - 1])
  await waitFor(() => expect(snapshotsApi.restoreSnapshot).toHaveBeenCalledWith('pre-upgrade'))
  // the candidate diff is refreshed so the user can review before committing
  await waitFor(() => expect(configApi.getConfigDiff).toHaveBeenCalled())
})

test('delete removes a snapshot after confirm', async () => {
  renderPage()
  const rows = await screen.findAllByRole('row')
  fireEvent.click(within(rows[1]).getByRole('button', { name: 'Delete' }))
  expect(screen.getByText(/Delete snapshot “pre-upgrade”\?/)).toBeInTheDocument()
  const deleteButtons = screen.getAllByRole('button', { name: 'Delete' })
  fireEvent.click(deleteButtons[deleteButtons.length - 1])
  await waitFor(() => expect(snapshotsApi.deleteSnapshot).toHaveBeenCalledWith('pre-upgrade'))
})
