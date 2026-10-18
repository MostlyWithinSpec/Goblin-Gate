// GeoIP UI pieces (task 095): the country multi-select picker used in address
// forms and the database status card shown on the Addresses page. Both talk to
// /api/v1/geoip via the objects API client.
import React, { useEffect, useMemo, useState } from 'react'
import { RefreshCw, X } from 'lucide-react'
import { getGeoIP, refreshGeoIP, type GeoIPCountry, type GeoIPStatus } from '../api/objects'

// flagEmoji builds the ISO 3166-1 alpha-2 flag from regional indicator symbols.
export function flagEmoji(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return ''
  const base = 0x1f1e6 - 65
  return String.fromCodePoint(base + code.charCodeAt(0), base + code.charCodeAt(1))
}

// CountryPicker: searchable multi-select of countries. Stores the picked ISO
// codes joined with "," in `value` (the address value field).
export function CountryPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [countries, setCountries] = useState<GeoIPCountry[]>([])
  const [loaded, setLoaded] = useState(false)
  const [q, setQ] = useState('')

  useEffect(() => {
    getGeoIP()
      .then((r) => {
        setCountries(r.countries ?? [])
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
  }, [])

  const selected = useMemo(() => value.split(',').filter(Boolean), [value])
  const byCode = useMemo(() => new Map(countries.map((c) => [c.code, c])), [countries])

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return countries
    return countries.filter((c) => c.name.toLowerCase().includes(needle) || c.code.toLowerCase().includes(needle))
  }, [countries, q])

  const add = (code: string) => {
    if (selected.includes(code)) return
    onChange([...selected, code].join(','))
  }
  const remove = (code: string) => {
    onChange(selected.filter((c) => c !== code).join(','))
  }

  return (
    <div className="mt-1">
      {selected.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {selected.map((code) => (
            <span key={code} className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs text-blue-800 dark:bg-blue-900 dark:text-blue-200">
              {flagEmoji(code)} {byCode.get(code)?.name ?? code}
              <button type="button" aria-label={`Remove ${code}`} onClick={() => remove(code)}>
                <X size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
      {loaded && (
        <>
          <input
            aria-label="Search countries"
            className="w-full rounded border border-gray-300 px-2 py-1 dark:border-gray-600 dark:bg-gray-700"
            placeholder="Search by name or code…"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <div className="mt-1 max-h-40 overflow-y-auto rounded border border-gray-200 dark:border-gray-600">
            {filtered.length === 0 && <p className="px-2 py-1 text-xs text-gray-500">No matches.</p>}
            {filtered.map((c) => (
              <button
                key={c.code}
                type="button"
                onClick={() => add(c.code)}
                className={`flex w-full items-center gap-2 px-2 py-1 text-left text-sm hover:bg-gray-50 dark:hover:bg-gray-700 ${selected.includes(c.code) ? 'text-gray-400' : ''}`}
              >
                <span>{flagEmoji(c.code)}</span>
                <span className="w-8 font-mono text-xs">{c.code}</span>
                <span>{c.name}</span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

// GeoIPStatusCard: database status shown above the address list (task 095).
export function GeoIPStatusCard() {
  const [status, setStatus] = useState<GeoIPStatus | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const load = () => {
    getGeoIP()
      .then((r) => setStatus(r.status))
      .catch((e: Error) => setErr(e.message))
  }
  useEffect(load, [])

  const refresh = async () => {
    setRefreshing(true)
    setErr(null)
    try {
      await refreshGeoIP()
      load()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setRefreshing(false)
    }
  }

  if (!status) return null
  return (
    <div className="mb-4 rounded border border-gray-200 bg-white p-3 text-sm shadow-sm dark:border-gray-600 dark:bg-gray-800" data-testid="geoip-status">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="font-medium">GeoIP database</span>
        {status.loaded ? (
          <>
            <span>{status.month ?? 'unknown month'}</span>
            {status.rows != null && <span>{status.rows.toLocaleString()} ranges</span>}
          </>
        ) : (
          <span className="text-yellow-700 dark:text-yellow-400">not loaded yet</span>
        )}
        {status.last_error && <span className="text-red-600">{status.last_error}</span>}
        <button
          type="button"
          onClick={refresh}
          disabled={refreshing}
          className="ml-auto flex items-center gap-1 rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:hover:bg-gray-700"
        >
          <RefreshCw size={12} className={refreshing ? 'animate-spin' : ''} /> Refresh now
        </button>
      </div>
      {err && <p role="alert" className="mt-1 text-xs text-red-600">{err}</p>}
      <p className="mt-1 text-xs text-gray-500">
        IP geolocation by{' '}
        <a href="https://db-ip.com" target="_blank" rel="noreferrer" className="text-blue-600 hover:underline">
          DB-IP
        </a>
        , licensed CC BY 4.0.
      </p>
    </div>
  )
}
