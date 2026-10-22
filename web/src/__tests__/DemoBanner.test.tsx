import { describe, expect, test, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/config', async (orig) => {
  const real = await orig<typeof import('../api/config')>()
  return { ...real, getSystemStatus: vi.fn() }
})
import * as configApi from '../api/config'
import { DemoBanner } from '../components/Layout'

const show = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <DemoBanner />
    </QueryClientProvider>,
  )

describe('DemoBanner', () => {
  test('shows on the public demo', async () => {
    vi.mocked(configApi.getSystemStatus).mockResolvedValue({ version: 'x', demo: true })
    show()
    expect(await screen.findByRole('note')).toHaveTextContent('resets every hour')
    expect(screen.getByRole('link', { name: 'Install Goblin Gate' })).toHaveAttribute('href', expect.stringContaining('summoning-a-goblin'))
  })

  test('stays away on a real box', async () => {
    vi.mocked(configApi.getSystemStatus).mockResolvedValue({ version: 'x' })
    show()
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.queryByRole('note')).toBeNull()
  })
})
