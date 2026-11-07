// Threat feeds (config "feeds", task 027/030): IP, domain and URL block lists
// downloaded by goblind. The table joins the config with the live status
// (GET /api/v1/feeds); a lookup box finds which feeds list an IP or name.
import React, { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { getFeedStatus, lookupFeeds, refreshFeed, type Feed, type FeedStatus } from '../../api/objects'
import { t } from '../../i18n'

export function validateFeed(f: Feed): Record<string, string> {
  const e: Record<string, string> = {}
  // validate.go checkFeedsAndAdmins / validFeedFormat
  if (!/^https?:\/\//.test(f.url)) e.url = t('feeds.errUrl')
  if (f.refresh_minutes && (f.refresh_minutes < 15 || f.refresh_minutes > 10080)) e.refresh_minutes = t('feeds.errRefresh')
  if (f.format && !/^(plain|csv:.+|json:.*)$/.test(f.format)) e.format = t('feeds.errFormat')
  if (f.auth_header && !/^[^:]*\S[^:]*:\s*\S/.test(f.auth_header)) e.auth_header = t('feeds.errAuthHeader')
  return e
}

const ago = (iso?: string) => {
  if (!iso) return t('feeds.never')
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000)
  return s < 120 ? t('feeds.agoSeconds', { seconds: Math.round(s) }) : s < 7200 ? t('feeds.agoMinutes', { minutes: Math.round(s / 60) }) : t('feeds.agoHours', { hours: Math.round(s / 3600) })
}

function Lookup() {
  const [q, setQ] = useState('')
  const [res, setRes] = useState<{ query: string; feeds: string[] } | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const go = (e: React.FormEvent) => {
    e.preventDefault()
    if (!q.trim()) return
    setErr(null)
    lookupFeeds(q.trim()).then(setRes).catch((x: Error) => setErr(x.message))
  }
  return (
    <form onSubmit={go} className="mt-6 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
      <h3 className="mb-2 font-semibold">{t('feeds.lookupTitle')}</h3>
      <div className="flex gap-2">
        <input aria-label="Lookup" className={inputClass + ' mt-0'} placeholder="203.0.113.7 or bad.example" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-white">{t('feeds.lookupButton')}</button>
      </div>
      {err && <p role="alert" className="mt-2 text-red-600">{err}</p>}
      {res && (
        <p className="mt-2" data-testid="lookup-result">
          {res.feeds.length === 0 ? <>{t('feeds.notListed')} <code>{res.query}</code>.</> : <><code>{res.query}</code> {t('feeds.listedBy')} {res.feeds.join(', ')}.</>}
        </p>
      )}
    </form>
  )
}

export default function ThreatFeeds() {
  const [status, setStatus] = useState<Record<string, FeedStatus>>({})
  const [busy, setBusy] = useState<string | null>(null)
  const load = useCallback(() => {
    getFeedStatus()
      .then((fs) => setStatus(Object.fromEntries(fs.map((f) => [f.name, f]))))
      .catch(() => {})
  }, [])
  useEffect(load, [load])

  const refresh = (name: string) => {
    setBusy(name)
    refreshFeed(name)
      .then((st) => setStatus((s) => ({ ...s, [name]: st })))
      .catch(() => {})
      .finally(() => setBusy(null))
  }

  return (
    <div className="p-6">
      <ConfigList<Feed>
        title={t('feeds.title')}
        path="feeds"
        newLabel={t('feeds.newFeed')}
        intro={t('feeds.intro')}
        select={(c) => c.feeds}
        empty={() => ({ name: '', type: 'ip', url: '', enabled: true })}
        clean={(f) => omitEmpty({ ...f, url: f.url.trim(), format: f.format?.trim(), auth_header: f.auth_header?.trim(), block_wan: f.type === 'ip' && f.block_wan, refresh_minutes: f.refresh_minutes || undefined })}
        validate={validateFeed}
        columns={[
          { label: t('feeds.colType'), render: (f) => f.type },
          { label: t('feeds.colEntries'), render: (f) => status[f.name]?.count ?? '—' },
          { label: t('feeds.colUpdated'), render: (f) => ago(status[f.name]?.fetched_at) },
          {
            label: t('feeds.colStatus'),
            render: (f) => {
              const st = status[f.name]
              if (!f.enabled) return <Badge variant="secondary">disabled</Badge>
              if (st?.error) return <span title={st.error}><Badge variant="error">error</Badge></span>
              if (!st) return <Badge variant="warning">{t('feeds.notApplied')}</Badge>
              return <Badge variant="success">ok</Badge>
            },
          },
          { label: t('feeds.colWanBlock'), render: (f) => (f.block_wan ? 'yes' : '') },
        ]}
        rowActions={(f) =>
          status[f.name] && (
            <button type="button" aria-label={`Refresh ${f.name}`} title={t('feeds.refreshNow')} disabled={busy === f.name} onClick={() => refresh(f.name)} className="text-blue-600 disabled:opacity-50">
              <RefreshCw size={16} className={busy === f.name ? 'animate-spin' : ''} />
            </button>
          )
        }
        form={(f, set, { err }) => (
          <>
            <label className="block">
              {t('feeds.type')}
              <select aria-label="Type" className={inputClass} value={f.type} onChange={(e) => set({ type: e.target.value as Feed['type'] })}>
                <option value="ip">{t('feeds.typeIp')}</option>
                <option value="domain">{t('feeds.typeDomains')}</option>
                <option value="url">{t('feeds.typeUrls')}</option>
              </select>
            </label>
            <label className="block">
              {t('feeds.url')}
              <input aria-label="URL" className={inputClass} placeholder="https://example.org/blocklist.txt" value={f.url} onChange={(e) => set({ url: e.target.value })} />
              {err('url')}
            </label>
            <label className="block">
              {t('feeds.format')}
              <input aria-label="Format" className={inputClass} placeholder="plain" value={f.format ?? ''} onChange={(e) => set({ format: e.target.value })} />
              {err('format')}
            </label>
            <label className="block">
              {t('feeds.refreshEvery')}
              <input aria-label="Refresh minutes" type="number" className={inputClass} placeholder="60" value={f.refresh_minutes ?? ''} onChange={(e) => set({ refresh_minutes: e.target.value ? Number(e.target.value) : undefined })} />
              {err('refresh_minutes')}
            </label>
            <label className="block">
              {t('feeds.authHeader')}
              <input aria-label="Auth header" className={inputClass} placeholder="Authorization: Bearer …" value={f.auth_header ?? ''} onChange={(e) => set({ auth_header: e.target.value })} />
              {err('auth_header')}
            </label>
            {f.type === 'ip' && (
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={!!f.block_wan} onChange={(e) => set({ block_wan: e.target.checked })} /> {t('feeds.blockWan')}
              </label>
            )}
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={f.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> {t('feeds.enabled')}
            </label>
          </>
        )}
        after={() => <Lookup />}
      />
    </div>
  )
}
