import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/system', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/system')>()
  return {
    ...real,
    getGeneratedFiles: vi.fn(),
    getGeneratedFile: vi.fn(),
    getGeneratedDiff: vi.fn(),
  }
})

import * as sys from '../api/system'
import GeneratedConfig from '../pages/system/GeneratedConfig'

const files = [
  { path: 'nftables.conf', renderer: 'nft', size: 1200, redacted: false },
  { path: 'wireguard/wg0.conf', renderer: 'wireguard', size: 300, redacted: true },
  { path: 'kea-dhcp4.json', renderer: 'kea', size: 500, redacted: false },
]

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(sys.getGeneratedFiles).mockReset().mockResolvedValue(structuredClone(files))
  vi.mocked(sys.getGeneratedFile).mockReset().mockResolvedValue({
    path: 'nftables.conf',
    renderer: 'nft',
    content: 'table inet gg {\n}\n',
    redacted: false,
    truncated: false,
  })
  vi.mocked(sys.getGeneratedDiff).mockReset().mockResolvedValue({ path: 'nftables.conf', diff: '' })
})

describe('Generated config page', () => {
  test('lists files grouped by renderer', async () => {
    wrap(<GeneratedConfig />)
    expect(await screen.findByText('nftables.conf')).toBeInTheDocument()
    expect(screen.getByText('wireguard/wg0.conf')).toBeInTheDocument()
    expect(screen.getByText('kea-dhcp4.json')).toBeInTheDocument()
    // Group headers, uppercased by the CSS class but present in the DOM text.
    expect(screen.getByText('nft')).toBeInTheDocument()
    expect(screen.getByText('wireguard')).toBeInTheDocument()
    expect(screen.getByText('kea')).toBeInTheDocument()
  })

  test('selecting a file loads its content with line numbers', async () => {
    wrap(<GeneratedConfig />)
    fireEvent.click(await screen.findByText('nftables.conf'))
    expect(await screen.findByText('table inet gg {')).toBeInTheDocument()
    // Line numbers are rendered.
    expect(screen.getByText('1')).toBeInTheDocument()
    expect(sys.getGeneratedFile).toHaveBeenCalledWith('nftables.conf', undefined)
  })

  test('shows the redacted note for redacted files', async () => {
    vi.mocked(sys.getGeneratedFile).mockResolvedValue({
      path: 'wireguard/wg0.conf',
      renderer: 'wireguard',
      content: 'PrivateKey = <redacted>',
      redacted: true,
      truncated: false,
    })
    wrap(<GeneratedConfig />)
    fireEvent.click(await screen.findByText('wireguard/wg0.conf'))
    expect(await screen.findByText(/Secrets are redacted/)).toBeInTheDocument()
  })

  test('candidate toggle passes config=candidate to the API', async () => {
    wrap(<GeneratedConfig />)
    await screen.findByText('nftables.conf')
    fireEvent.click(screen.getByRole('button', { name: 'candidate' }))
    await waitFor(() => expect(sys.getGeneratedFiles).toHaveBeenCalledWith('candidate'))
    // Selecting a file in candidate mode also passes the config.
    fireEvent.click(screen.getByText('nftables.conf'))
    await waitFor(() => expect(sys.getGeneratedFile).toHaveBeenCalledWith('nftables.conf', 'candidate'))
  })

  test('diff view colours added and removed lines', async () => {
    vi.mocked(sys.getGeneratedDiff).mockResolvedValue({
      path: 'nftables.conf',
      diff: [
        '--- running/nftables.conf',
        '+++ candidate/nftables.conf',
        '@@ -1,2 +1,3 @@',
        ' table inet gg {',
        '- chain input {',
        '+ chain forward {',
        '+   ct state established accept',
      ].join('\n'),
    })
    wrap(<GeneratedConfig />)
    fireEvent.click(screen.getByRole('button', { name: 'candidate' }))
    await waitFor(() => expect(sys.getGeneratedFiles).toHaveBeenCalledWith('candidate'))
    fireEvent.click(await screen.findByText('nftables.conf'))

    // Diff button only exists in candidate mode.
    const diffBtn = await screen.findByRole('button', { name: /Diff vs running/ })
    fireEvent.click(diffBtn)

    const diffEl = await screen.findByTestId('unified-diff')
    expect(diffEl).toBeInTheDocument()
    // Removed line is red with strikethrough.
    const removed = screen.getByText('- chain input {')
    expect(removed.className).toContain('bg-red-50')
    expect(removed.className).toContain('line-through')
    // Added lines are green.
    const added = screen.getByText('+ chain forward {')
    expect(added.className).toContain('bg-green-50')
    expect(screen.getByText(/\+ ct state established accept/).className).toContain('bg-green-50')
    expect(sys.getGeneratedDiff).toHaveBeenCalledWith('nftables.conf')
  })

  test('diff shows a note when there are no changes', async () => {
    wrap(<GeneratedConfig />)
    fireEvent.click(screen.getByRole('button', { name: 'candidate' }))
    await waitFor(() => expect(sys.getGeneratedFiles).toHaveBeenCalledWith('candidate'))
    fireEvent.click(await screen.findByText('nftables.conf'))
    fireEvent.click(await screen.findByRole('button', { name: /Diff vs running/ }))
    expect(await screen.findByTestId('diff-unchanged')).toBeInTheDocument()
  })

  test('search filters the file list', async () => {
    wrap(<GeneratedConfig />)
    await screen.findByText('nftables.conf')
    fireEvent.change(screen.getByLabelText('Filter files'), { target: { value: 'wireguard' } })
    expect(await screen.findByText('wireguard/wg0.conf')).toBeInTheDocument()
    expect(screen.queryByText('nftables.conf')).not.toBeInTheDocument()
    expect(screen.queryByText('kea-dhcp4.json')).not.toBeInTheDocument()
  })
})
