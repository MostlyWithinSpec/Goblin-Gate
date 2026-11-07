// Log & Report → Export (task 097): forward firewall logs to a remote syslog
// server or a Loki instance. The list lives in candidate.log_export; the
// status column comes from GET /api/v1/log-export, refreshed every 30 s.
import { useQuery } from '@tanstack/react-query'
import { ConfigList, Checklist, inputClass, omitEmpty, type Column, type FormCtx } from '../../components/ConfigList'
import type { LogExport, LogExportStatus } from '../../api/objects'
import { getLogExportStatus } from '../../api/objects'
import { t } from '../../i18n'
import { validateLogExport } from '../network/networkLogic'

const KINDS = ['traffic', 'ips', 'dns', 'webfilter', 'system']

// relTime renders an RFC3339 time as a short relative age.
function relTime(s?: string): string {
  if (!s) return '—'
  const at = Date.parse(s)
  if (Number.isNaN(at)) return '—'
  const sec = Math.max(0, (Date.now() - at) / 1000)
  if (sec < 60) return t('logexport.justNow')
  if (sec < 3600) return t('devices.minutesAgo', { count: Math.floor(sec / 60) })
  if (sec < 86400) return t('devices.hoursAgo', { count: Math.floor(sec / 3600) })
  return t('devices.daysAgo', { count: Math.floor(sec / 86400) })
}

export default function LogExportPage() {
  const q = useQuery({ queryKey: ['log-export-status'], queryFn: getLogExportStatus, refetchInterval: 30_000 })
  const byName = new Map<string, LogExportStatus>((q.data ?? []).map((e) => [e.name, e]))

  const columns: Column<LogExport>[] = [
    { label: t('logexport.col.enabled'), render: (d) => <span className={d.enabled ? '' : 'text-gray-400'}>{d.enabled ? t('logexport.yes') : t('logexport.no')}</span> },
    { label: t('logexport.col.type'), render: (d) => <span className="capitalize">{d.type}</span> },
    {
      label: t('logexport.col.target'),
      render: (d) => (
        <span className="text-gray-500">
          {d.type === 'syslog' ? `${d.protocol ?? 'udp'} → ${d.address ?? ''}` : d.url ?? ''}
        </span>
      ),
    },
    {
      label: t('logexport.col.status'),
      render: (d) => {
        const s = byName.get(d.name)
        if (!s) return <span className="text-gray-400">—</span>
        return (
          <div className="space-y-0.5 text-xs">
            <div>
              {t('logexport.sent')} <b>{s.sent}</b>
              {s.dropped > 0 && (
                <span className="ml-2 text-amber-600 dark:text-amber-400">
                  {t('logexport.dropped')} <b>{s.dropped}</b>
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
      title={t('logexport.title')}
      intro={t('logexport.intro')}
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
            {t('logexport.type')}
            <select aria-label={t('logexport.type')} className={inputClass} value={item.type} onChange={(e) => set({ type: e.target.value as LogExport['type'] })}>
              <option value="syslog">Syslog</option>
              <option value="loki">Loki</option>
            </select>
          </label>
          {ctx.err('type')}
          <Checklist
            label={t('logexport.kinds')}
            options={KINDS}
            value={item.kinds ?? []}
            onChange={(kinds) => set({ kinds })}
          />
          {ctx.err('kinds')}
          {item.type === 'syslog' ? (
            <>
              <label className="block text-sm font-medium">
                {t('logexport.address')}
                <input
                  className={inputClass}
                  placeholder="10.0.0.2:514"
                  value={item.address ?? ''}
                  onChange={(e) => set({ address: e.target.value })}
                />
              </label>
              {ctx.err('address')}
              <label className="block text-sm font-medium">
                {t('logexport.protocol')}
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
                {t('logexport.url')}
                <input
                  className={inputClass}
                  placeholder="https://loki.example.com/loki/api/v1/push"
                  value={item.url ?? ''}
                  onChange={(e) => set({ url: e.target.value })}
                />
              </label>
              {ctx.err('url')}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <label className="block text-sm font-medium">
                  {t('logexport.username')}
                  <input
                    className={inputClass}
                    value={item.username ?? ''}
                    onChange={(e) => set({ username: e.target.value })}
                  />
                </label>
                <label className="block text-sm font-medium">
                  {t('logexport.password')}
                  <input
                    type="password"
                    className={inputClass}
                    placeholder={ctx.isNew ? '' : t('logexport.unchanged')}
                    value={item.password ?? ''}
                    onChange={(e) => set({ password: e.target.value })}
                  />
                </label>
              </div>
              {ctx.err('password')}
              <label className="block text-sm font-medium">
                {t('logexport.token')}
                <input
                  type="password"
                  className={inputClass}
                  placeholder={ctx.isNew ? '' : t('logexport.unchanged')}
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
