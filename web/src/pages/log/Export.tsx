// Log & Report → Export (task 097): forward firewall logs to a remote syslog
// server or a Loki instance. The list lives in candidate.log_export; the
// status column comes from GET /api/v1/log-export, refreshed every 30 s.
import { useQuery } from '@tanstack/react-query'
import { ConfigList, Checklist, inputClass, omitEmpty, type Column, type FormCtx } from '../../components/ConfigList'
import type { LogExport, LogExportStatus } from '../../api/objects'
import { getLogExportStatus } from '../../api/objects'
import { validateLogExport } from '../network/networkLogic'

const KINDS = ['traffic', 'ips', 'dns', 'webfilter', 'system']

// relTime renders an RFC3339 time as a short relative age.
function relTime(s?: string): string {
  if (!s) return '—'
  const t = Date.parse(s)
  if (Number.isNaN(t)) return '—'
  const sec = Math.max(0, (Date.now() - t) / 1000)
  if (sec < 60) return 'just now'
  if (sec < 3600) return `${Math.floor(sec / 60)} min ago`
  if (sec < 86400) return `${Math.floor(sec / 3600)} h ago`
  return `${Math.floor(sec / 86400)} d ago`
}

export default function LogExportPage() {
  const q = useQuery({ queryKey: ['log-export-status'], queryFn: getLogExportStatus, refetchInterval: 30_000 })
  const byName = new Map<string, LogExportStatus>((q.data ?? []).map((e) => [e.name, e]))

  const columns: Column<LogExport>[] = [
    { label: 'Enabled', render: (d) => <span className={d.enabled ? '' : 'text-gray-400'}>{d.enabled ? 'Yes' : 'No'}</span> },
    { label: 'Type', render: (d) => <span className="capitalize">{d.type}</span> },
    {
      label: 'Target',
      render: (d) => (
        <span className="text-gray-500">
          {d.type === 'syslog' ? `${d.protocol ?? 'udp'} → ${d.address ?? ''}` : d.url ?? ''}
        </span>
      ),
    },
    {
      label: 'Status',
      render: (d) => {
        const s = byName.get(d.name)
        if (!s) return <span className="text-gray-400">—</span>
        return (
          <div className="space-y-0.5 text-xs">
            <div>
              sent <b>{s.sent}</b>
              {s.dropped > 0 && (
                <span className="ml-2 text-amber-600 dark:text-amber-400">
                  dropped <b>{s.dropped}</b>
                </span>
              )}
            </div>
            <div className="text-gray-500">{relTime(s.last_sent)}</div>
            {s.last_error && <div className="text-red-600 dark:text-red-400">{s.last_error}</div>}
          </div>
        )
      },
    },
  ]

  return (
    <ConfigList<LogExport>
      title="Log export"
      intro="Forward firewall logs to a remote syslog server or a Loki instance."
      path="log_export"
      select={(cfg) => cfg.log_export}
      empty={() => ({ name: '', enabled: true, type: 'syslog' })}
      columns={columns}
      // Secrets arrive redacted (empty) for non-secret-seers; dropping an empty
      // one keeps the stored value on the server.
      clean={(d) => {
        const out = { ...d }
        if (!out.username?.trim()) delete out.username
        if (!out.password?.trim()) delete out.password
        if (!out.token?.trim()) delete out.token
        return omitEmpty(out) as LogExport
      }}
      validate={(item, cfg) => validateLogExport(item, (cfg.log_export ?? []).filter((d) => d.name !== item.name))}
      form={(item, set, ctx: FormCtx) => (
        <div className="space-y-3">
          <label className="block text-sm font-medium">
            Type
            <select aria-label="Type" className={inputClass} value={item.type} onChange={(e) => set({ type: e.target.value as LogExport['type'] })}>
              <option value="syslog">Syslog</option>
              <option value="loki">Loki</option>
            </select>
          </label>
          {ctx.err('type')}
          <Checklist
            label="Log kinds (none = all)"
            options={KINDS}
            value={item.kinds ?? []}
            onChange={(kinds) => set({ kinds })}
          />
          {ctx.err('kinds')}
          {item.type === 'syslog' ? (
            <>
              <label className="block text-sm font-medium">
                Address
                <input
                  className={inputClass}
                  placeholder="10.0.0.2:514"
                  value={item.address ?? ''}
                  onChange={(e) => set({ address: e.target.value })}
                />
              </label>
              {ctx.err('address')}
              <label className="block text-sm font-medium">
                Protocol
                <select
                  className={inputClass}
                  value={item.protocol ?? 'udp'}
                  onChange={(e) => set({ protocol: e.target.value as LogExport['protocol'] })}
                >
                  <option value="udp">UDP</option>
                  <option value="tcp">TCP</option>
                  <option value="tls">TLS</option>
                </select>
              </label>
              {ctx.err('protocol')}
            </>
          ) : (
            <>
              <label className="block text-sm font-medium">
                URL
                <input
                  className={inputClass}
                  placeholder="https://loki.example.com/loki/api/v1/push"
                  value={item.url ?? ''}
                  onChange={(e) => set({ url: e.target.value })}
                />
              </label>
              {ctx.err('url')}
              <div className="grid grid-cols-2 gap-3">
                <label className="block text-sm font-medium">
                  Username (basic auth, optional)
                  <input
                    className={inputClass}
                    value={item.username ?? ''}
                    onChange={(e) => set({ username: e.target.value })}
                  />
                </label>
                <label className="block text-sm font-medium">
                  Password (basic auth, optional)
                  <input
                    type="password"
                    className={inputClass}
                    placeholder={ctx.isNew ? '' : 'unchanged'}
                    value={item.password ?? ''}
                    onChange={(e) => set({ password: e.target.value })}
                  />
                </label>
              </div>
              {ctx.err('password')}
              <label className="block text-sm font-medium">
                Bearer token (optional, instead of basic auth)
                <input
                  type="password"
                  className={inputClass}
                  placeholder={ctx.isNew ? '' : 'unchanged'}
                  value={item.token ?? ''}
                  onChange={(e) => set({ token: e.target.value })}
                />
              </label>
              {ctx.err('token')}
            </>
          )}
        </div>
      )}
    />
  )
}
