import { describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn() }
})
import * as api from '../api/objects'
import { SearchModal } from '../components/SearchModal'

function Where() {
  const l = useLocation()
  return <p data-testid="where">{l.pathname + l.search}</p>
}

describe('SearchModal', () => {
  test('typing ranks objects and Enter opens the first', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({ addresses: [{ name: 'nas', type: 'host', value: '192.168.1.5' }] })
    const onClose = vi.fn()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <SearchModal isOpen onClose={onClose} />
          <Routes>
            <Route path="*" element={<Where />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const input = screen.getByLabelText('Search pages and objects')
    fireEvent.change(input, { target: { value: 'nas' } })
    expect(await screen.findByText('192.168.1.5')).toBeInTheDocument()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByTestId('where')).toHaveTextContent('/network/addresses?edit=nas')
    expect(onClose).toHaveBeenCalled()
  })
})
