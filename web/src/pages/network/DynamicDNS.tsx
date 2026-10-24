import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import type { DDNS, DDNSStatus } from '../../api/objects'
import { getDDNSStatus, updateDDNSNow } from '../../api/objects'
import { ConfigList, inputClass, omitEmpty, type Column, type FormCtx } from '../../components/ConfigList'
import { wanInterfaces, validateDDNS } from './networkLogic'

function relTime(s?: string): string {
  if (!s) return '—'
  const d = Date.now() - new Date(s).getTime()
  if (d < 60_000) return 'just now'
  if (d < 3_600_000) return `${Math.floor(d / 60_000)} min ago`
  if (d < 86_400_000) return `${Math.floor(d / 3_600_000)} h ago`
  return `${Math.floor(d / 86_400_000)} d ago`
}

// UpdateNow asks the box to update one entry now and says how it went (an
// entry that is not committed yet is unknown to the box, which it says).
function UpdateNow({ name, onDone }: { name: string; onDone: () => void }) {
  const [state, setState] = useState<{ busy: boolean; ok?: boolean; msg?: string }>({ busy: false })
  const run = () => {
    setState({ busy: true })
    updateDDNSNow(name)
      .then(() => {
        setState({ busy: false, ok: true, msg: 'updated' })
        onDone()
      })
      .catch((e: Error) => setState({ busy: false, ok: false, msg: e.message }))
  }
  return (
    <span className="mr-2 inline-flex items-center gap-1 align-middle">
      <button type="button" disabled={state.busy} onClick={run} className="inline-flex items-center gap-1 text-xs text-blue-600 hover:underline disabled:opacity-50">
        <RefreshCw size={12} className={state.busy ? 'animate-spin' : ''} /> Update now
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
    { label: 'Provider', render: (d) => d.provider },
    { label: 'Hostname', render: (d) => d.hostname },
    { label: 'Interface', render: (d) => d.interface ?? 'wan1' },
    {
      label: 'Status',
      render: (d) => {
        const s = byName.get(d.name)
        if (!s) return <span className="text-gray-400">—</span>
        return (
          <div className="space-y-0.5 text-xs">
            <div>{s.ip ?? 'no IP yet'}</div>
            <div className="text-gray-500">{relTime(s.last_update)}</div>
            {s.last_error && <div className="text-red-600">{s.last_error}</div>}
          </div>
        )
      },
    },
  ]

  return (
    <ConfigList<DDNS>
      title="Dynamic DNS"
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
      newLabel="New entry"
      form={(item, set, ctx: FormCtx) => {
        const wan = wanInterfaces(ctx.cfg)
        return (
          <>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={item.enabled !== false} onChange={(e) => set({ enabled: e.target.checked })} /> Enabled
            </label>
            <label className="block">
              Provider
              <select aria-label="Provider" className={inputClass} value={item.provider} onChange={(e) => set({ provider: e.target.value as DDNS['provider'] })}>
                <option value="cloudflare">Cloudflare</option>
                <option value="duckdns">DuckDNS</option>
                <option value="custom">Custom URL</option>
              </select>
            </label>
            <label className="block">
              Hostname
              <input aria-label="Hostname" placeholder="vpn.example.com" className={inputClass} value={item.hostname ?? ''} onChange={(e) => set({ hostname: e.target.value })} />
              {ctx.err('hostname')}
            </label>
            <label className="block">
              WAN interface
              <select aria-label="WAN interface" className={inputClass} value={item.interface ?? ''} onChange={(e) => set({ interface: e.target.value || undefined })}>
                <option value="">wan1 (default)</option>
                {wan.map((w) => <option key={w} value={w}>{w}</option>)}
              </select>
            </label>
            {item.provider === 'cloudflare' && (
              <>
                <label className="block">
                  API token
                  <input aria-label="API token" type="password" placeholder={ctx.isNew ? '' : 'unchanged'} className={inputClass} value={item.token ?? ''} onChange={(e) => set({ token: e.target.value })} />
                  {ctx.err('token')}
                  <p className="mt-1 text-xs text-gray-500">an API token with Zone → DNS → Edit</p>
                </label>
                <label className="block">
                  Zone ID
                  <input aria-label="Zone ID" className={inputClass} value={item.zone_id ?? ''} onChange={(e) => set({ zone_id: e.target.value })} />
                  {ctx.err('zone_id')}
                </label>
              </>
            )}
            {item.provider === 'duckdns' && (
              <label className="block">
                Token
                <input aria-label="Token" type="password" placeholder={ctx.isNew ? '' : 'unchanged'} className={inputClass} value={item.token ?? ''} onChange={(e) => set({ token: e.target.value })} />
                {ctx.err('token')}
              </label>
            )}
            {item.provider === 'custom' && (
              <label className="block">
                Update URL
                <input aria-label="Update URL" placeholder="https://example.com/update?ip={ip}" className={inputClass} value={item.url ?? ''} onChange={(e) => set({ url: e.target.value })} />
                {ctx.err('url')}
                <p className="mt-1 text-xs text-gray-500">Must start with https:// and contain {'{ip}'}</p>
              </label>
            )}
          </>
        )
      }}
    />
  )
}
