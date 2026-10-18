import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getGeoIP: vi.fn(),
    refreshGeoIP: vi.fn(),
    geoipLookup: vi.fn(),
  }
})

import * as api from '../api/objects'
import Addresses from '../pages/network/Addresses'
import { CountryPicker, GeoIPStatusCard, flagEmoji } from '../components/GeoIP'

const cfg: Candidate = {
  interfaces: [{ name: 'wan1', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true }],
  vlans: [],
  addresses: [
    { name: 'nas', type: 'host', value: '192.168.1.5' },
    { name: 'eu', type: 'country', value: 'DE,FR' },
  ],
  address_groups: [],
  services: [],
  policies: [],
  feeds: [],
  profiles: {},
}

const countries = [
  { code: 'CN', name: 'China' },
  { code: 'DE', name: 'Germany' },
  { code: 'FR', name: 'France' },
  { code: 'US', name: 'United States' },
]

const geoipStatus = {
  loaded: true,
  month: '2026-09',
  rows: 812345,
  fetched: '2026-09-01T00:00:00Z',
  source: 'db-ip',
  last_error: '',
  last_check: '2026-09-15T12:00:00Z',
  used: ['CN'],
  attribution: 'DB-IP public database',
}

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getGeoIP).mockReset().mockResolvedValue({ status: geoipStatus, countries })
  vi.mocked(api.refreshGeoIP).mockReset().mockResolvedValue(geoipStatus)
  vi.mocked(api.geoipLookup).mockReset().mockResolvedValue({ ip: '1.2.3.4', country: 'US', name: 'United States' })
})

describe('flagEmoji', () => {
  test('builds the flag from a two-letter code', () => {
    expect(flagEmoji('DE')).toBe('\u{1f1e9}\u{1f1ea}')
    expect(flagEmoji('US')).toBe('\u{1f1fa}\u{1f1f8}')
  })

  test('returns empty string for invalid codes', () => {
    expect(flagEmoji('X')).toBe('')
    expect(flagEmoji('ABC')).toBe('')
    expect(flagEmoji('ab')).toBe('')
  })
})

describe('CountryPicker', () => {
  test('shows selected countries as chips and allows removing them', async () => {
    const onChange = vi.fn()
    render(<CountryPicker value="DE,FR" onChange={onChange} />)
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())

    // Both selected countries appear as chips
    expect(screen.getByText('Germany')).toBeInTheDocument()
    expect(screen.getByText('France')).toBeInTheDocument()

    // Remove Germany
    fireEvent.click(screen.getByRole('button', { name: 'Remove DE' }))
    expect(onChange).toHaveBeenCalledWith('FR')
  })

  test('search filters by name and code', async () => {
    render(<CountryPicker value="" onChange={() => {}} />)
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())

    // All four countries visible initially
    expect(screen.getByText('China')).toBeInTheDocument()
    expect(screen.getByText('Germany')).toBeInTheDocument()
    expect(screen.getByText('France')).toBeInTheDocument()
    expect(screen.getByText('United States')).toBeInTheDocument()

    // Search by name
    fireEvent.change(screen.getByLabelText('Search countries'), { target: { value: 'germ' } })
    expect(screen.getByText('Germany')).toBeInTheDocument()
    expect(screen.queryByText('China')).not.toBeInTheDocument()

    // Search by code
    fireEvent.change(screen.getByLabelText('Search countries'), { target: { value: 'US' } })
    expect(screen.getByText('United States')).toBeInTheDocument()
    expect(screen.queryByText('Germany')).not.toBeInTheDocument()
  })

  test('clicking a country adds it to the selection', async () => {
    const onChange = vi.fn()
    render(<CountryPicker value="DE" onChange={onChange} />)
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())

    // Add France by clicking it in the list
    fireEvent.click(screen.getByText('France'))
    expect(onChange).toHaveBeenCalledWith('DE,FR')
  })

  test('empty selection is allowed (validation is done by the form)', async () => {
    const onChange = vi.fn()
    render(<CountryPicker value="" onChange={onChange} />)
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())

    // No chips rendered when empty
    expect(screen.queryByRole('button', { name: /^Remove / })).not.toBeInTheDocument()
  })
})

describe('GeoIPStatusCard', () => {
  test('shows database info and attribution link', async () => {
    render(<GeoIPStatusCard />)
    const card = await screen.findByTestId('geoip-status')
    expect(within(card).getByText('2026-09')).toBeInTheDocument()
    expect(within(card).getByText(/812,345 ranges/)).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'DB-IP' })).toHaveAttribute('href', 'https://db-ip.com')
  })

  test('refresh button calls refreshGeoIP and reloads status', async () => {
    render(<GeoIPStatusCard />)
    const card = await screen.findByTestId('geoip-status')
    fireEvent.click(within(card).getByRole('button', { name: /Refresh now/ }))

    await waitFor(() => expect(api.refreshGeoIP).toHaveBeenCalled())
    // After refresh, status is re-fetched (getGeoIP called again)
    await waitFor(() => expect(api.getGeoIP).toHaveBeenCalledTimes(2))
  })

  test('shows last error when present', async () => {
    vi.mocked(api.getGeoIP).mockResolvedValue({ status: { ...geoipStatus, last_error: 'download failed' }, countries })
    render(<GeoIPStatusCard />)
    const card = await screen.findByTestId('geoip-status')
    expect(within(card).getByText('download failed')).toBeInTheDocument()
  })

  test('shows "not loaded yet" when database is not loaded', async () => {
    vi.mocked(api.getGeoIP).mockResolvedValue({ status: { ...geoipStatus, loaded: false }, countries })
    render(<GeoIPStatusCard />)
    const card = await screen.findByTestId('geoip-status')
    expect(within(card).getByText(/not loaded yet/)).toBeInTheDocument()
  })
})

describe('Addresses page with country type', () => {
  test('renders country address as chips in the list', async () => {
    wrap(<Addresses />)
    // The "eu" address with value "DE,FR" should show flag chips
    await screen.findByRole('button', { name: 'eu' })
    expect(screen.getByText((t) => t.includes('DE'))).toBeInTheDocument()
    expect(screen.getByText((t) => t.includes('FR'))).toBeInTheDocument()
  })

  test('country picker is shown in the form when type is country', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'eu' })
    fireEvent.click(screen.getByRole('button', { name: 'eu' }))
    // The type select should be on "country"
    const typeSelect = screen.getByLabelText('Type') as HTMLSelectElement
    expect(typeSelect.value).toBe('country')
    // CountryPicker search input is visible
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())
  })

  test('saving a country address with no countries fails validation', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'nas' })
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'test-country' } })
    // Change type to country
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'country' } })
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())
    // Don't pick any country, just save
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Pick at least one country/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('saving a country address with countries works', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'nas' })
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'test-country' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'country' } })
    await waitFor(() => expect(screen.getByLabelText('Search countries')).toBeInTheDocument())
    // Pick a country
    fireEvent.click(screen.getByText('United States'))
    // Save
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
  })
})
