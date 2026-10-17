import { beforeEach, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getRevisions: vi.fn(), rollbackRevisionTo: vi.fn() }
})

import * as configApi from '../api/config'
import { Revisions } from '../pages/Revisions'

const renderPage = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Revisions />
    </QueryClientProvider>,
  )

beforeEach(() => {
  // the API's field names: revision and time (RFC 3339), newest first
  vi.mocked(configApi.getRevisions).mockReset().mockResolvedValue({
    revisions: [
      { revision: 3, time: '2026-10-16T14:05:00Z', user: 'admin', comment: 'import' },
      { revision: 2, time: '2026-10-15T09:30:00Z', user: 'admin' },
    ],
  })
  vi.mocked(configApi.rollbackRevisionTo).mockReset().mockResolvedValue({ revision: 4 })
})

test('shows revision numbers and real dates', async () => {
  renderPage()
  const rows = await screen.findAllByRole('row')
  expect(rows).toHaveLength(3)
  expect(within(rows[1]).getByText('3')).toBeInTheDocument()
  expect(within(rows[1]).getByText('running')).toBeInTheDocument()
  expect(within(rows[1]).getByText(/2026/)).toBeInTheDocument()
  expect(screen.queryByText(/Invalid Date/)).not.toBeInTheDocument()
  // the running revision cannot be rolled back to
  expect(within(rows[1]).queryByRole('button', { name: 'Rollback' })).not.toBeInTheDocument()
})

test('rolls back to the chosen revision', async () => {
  renderPage()
  const rows = await screen.findAllByRole('row')
  fireEvent.click(within(rows[2]).getByRole('button', { name: 'Rollback' }))
  expect(screen.getByText('Roll back to revision #2?')).toBeInTheDocument()
  const buttons = screen.getAllByRole('button', { name: 'Rollback' })
  fireEvent.click(buttons[buttons.length - 1])
  await waitFor(() => expect(configApi.rollbackRevisionTo).toHaveBeenCalledWith(2))
})
