import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import type { DDNS, DDNSStatus } from '../../api/objects'
import { getDDNSStatus, updateDDNSNow } from '../../api/objects'
import { ConfigList, inputClass, omitEmpty, type Column, type FormCtx } from '../../components/ConfigList'
import { t } from '../../i18n'
import { wanInterfaces, validateDDNS } from './networkLogic'

function relTime(s?: string): string {
  if (!s) return '—'
  const d = Date.now() - new Date(s).getTime()
  if (d < 60_000) return t('ddns.justNow')
  if (d < 3_600_000) return t('ddns.minutesAgo', { count: Math.floor(d / 60_000) })
  if (d < 86_400_000) return t('ddns.hoursAgo', { count: Math.floor(d / 3_600_000) })
  return t('ddns.daysAgo', { count: Math.floor(d / 86_400_000) })
}

// UpdateNow asks the box to update one entry now and says how it went (an
// entry that is not committed yet is unknown to the box, which it says).
function UpdateNow({ name, onDone }: { name: string; onDone: () => void }) {
  const [state, setState] = useState<{ busy: boolean; ok?: boolean; msg?: string }>({ busy: false })
  const run = () => {
    setState({ busy: true })
    updateDDNSNow(name)
      .then(() => {
        setState({ busy: false, ok: true, msg: t('ddns.updated') })
        onDone()
      })
      .catch((e: Error) => setState({ busy: false, ok: false, msg: e.message }))
  }
  return (
    <span className="mr-2 inline-flex items-center gap-1 align-middle">
      <button type="button" disabled={state.busy} onClick={run} className="inline-flex items-center gap-1 text-xs text-blue-600 hover:underline disabled:opacity-50">
        <RefreshCw size={12} className={state.busy ? 'animate-spin' : ''} /> {t('ddns.updateNow')}
      </button>
      {state.msg && (
        <span role={state.ok ? 'status' : 'alert'} className={`max-w-[14rem] truncate text-xs ${state.ok ? 'text-green-600' : 'text-red-600'}`} title={state.msg}>
          {state.msg}
        </span>
      )}
    </span>
  )
}

export default function DynamicDNS() {
  const q = useQuery({ queryKey: ['ddns-status'], queryFn: getDDNSStatus, refetchInterval: 30_000 })
  const byName = new Map<string, DDNSStatus>((q.data ?? []).map((e) => [e.name, e]))

  const columns: Column<DDNS>[] = [
    { label: t('ddns.col.provider'), render: (d) => d.provider },
    { label: t('ddns.col.hostname'), render: (d) => d.hostname },
    { label: t('ddns.col.interface'), render: (d) => d.interface ?? 'wan1' },
    {
      label: t('ddns.col.status'),
      render: (d) => {
        const s = byName.get(d.name)
        if (!s) return <span className="text-gray-400">—</span>
        return (
          <div className="space-y-0.5 text-xs">
            <div>{s.ip ?? t('ddns.noIpYet')}</div>
            <div className="text-gray-500">{relTime(s.last_update)}</div>
            {s.last_error && <div className="text-red-600">{s.last_error}</div>}
          </div>
        )
      },
    },
  ]

  return (
    <ConfigList<DDNS>
      title={t('ddns.title')}
      path="ddns"
      select={(cfg) => cfg.ddns}
      empty={() => ({ name: '', enabled: true, provider: 'cloudflare', hostname: '' })}
      columns={columns}
      rowActions={(d) => <UpdateNow name={d.name} onDone={() => q.refetch()} />}
      validate={(item, cfg) => {
        const isNew = !(cfg.ddns ?? []).some((d) => d.name === item.name)
        return validateDDNS(item, wanInterfaces(cfg), (cfg.ddns ?? []).filter((d) => d.name !== item.name), isNew)
      }}
      // Admins get the stored token with the candidate, so editing keeps it;
      // a token cleared by hand is dropped and the server asks for one.
      clean={(d) => {
        const out = { ...d }
        if (!out.token?.trim()) delete out.token
        return omitEmpty(out) as DDNS
      }}
      newLabel={t('ddns.newEntry')}
      form={(item, set, ctx: FormCtx) => {
        const wan = wanInterfaces(ctx.cfg)
        const unchanged = ctx.isNew ? '' : t('ddns.unchanged')
        return (
          <>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={item.enabled !== false} onChange={(e) => set({ enabled: e.target.checked })} /> {t('ddns.enabled')}
            </label>
            <label className="block">
              {t('ddns.provider')}
              <select aria-label={t('ddns.provider')} className={inputClass} value={item.provider} onChange={(e) => set({ provider: e.target.value as DDNS['provider'] })}>
                <option value="cloudflare">Cloudflare</option>
                <option value="duckdns">DuckDNS</option>
                <option value="custom">{t('ddns.provider.custom')}</option>
              </select>
            </label>
            <label className="block">
              {t('ddns.hostname')}
              <input aria-label={t('ddns.hostname')} placeholder={t('ddns.hostnamePlaceholder')} className={inputClass} value={item.hostname ?? ''} onChange={(e) => set({ hostname: e.target.value })} />
              {ctx.err('hostname')}
            </label>
            <label className="block">
              {t('ddns.wanInterface')}
              <select aria-label={t('ddns.wanInterface')} className={inputClass} value={item.interface ?? ''} onChange={(e) => set({ interface: e.target.value || undefined })}>
                <option value="">{t('ddns.wanDefault')}</option>
                {wan.map((w) => <option key={w} value={w}>{w}</option>)}
              </select>
            </label>
            {item.provider === 'cloudflare' && (
              <>
                <label className="block">
                  {t('ddns.apiToken')}
                  <input aria-label={t('ddns.apiToken')} type="password" placeholder={unchanged} className={inputClass} value={item.token ?? ''} onChange={(e) => set({ token: e.target.value })} />
                  {ctx.err('token')}
                  <p className="mt-1 text-xs text-gray-500">{t('ddns.apiTokenNote')}</p>
                </label>
                <label className="block">
                  {t('ddns.zoneId')}
                  <input aria-label={t('ddns.zoneId')} className={inputClass} value={item.zone_id ?? ''} onChange={(e) => set({ zone_id: e.target.value })} />
                  {ctx.err('zone_id')}
                </label>
              </>
            )}
            {item.provider === 'duckdns' && (
              <label className="block">
                {t('ddns.token')}
                <input aria-label={t('ddns.token')} type="password" placeholder={unchanged} className={inputClass} value={item.token ?? ''} onChange={(e) => set({ token: e.target.value })} />
                {ctx.err('token')}
              </label>
            )}
            {item.provider === 'custom' && (
              <label className="block">
                {t('ddns.updateUrl')}
                <input aria-label={t('ddns.updateUrl')} placeholder={t('ddns.updateUrlPlaceholder')} className={inputClass} value={item.url ?? ''} onChange={(e) => set({ url: e.target.value })} />
                {ctx.err('url')}
                <p className="mt-1 text-xs text-gray-500">
                  {t('ddns.updateUrlNote')} {'{ip}'}
                </p>
              </label>
            )}
          </>
        )
      }}
    />
  )
}
