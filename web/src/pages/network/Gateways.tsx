// Gateways (config "gateways"): one per WAN interface/VLAN or WireGuard
// tunnel, with a monitor ping and live up/down status.
import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ConfigList, inputClass, type FormCtx } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { patchSection, type Candidate, type Gateway, type GatewayMonitor } from '../../api/objects'
import { getGatewayStatus, type TargetStatus } from '../../api/gateways'
import { t } from '../../i18n'
import { validateGateway, wanInterfaces, wireGuardClients } from './networkLogic'

function Num({ label, value, onChange, min, max }: { label: string; value: number | undefined; onChange: (v: number | undefined) => void; min?: number; max?: number }) {
  return (
    <label className="block">
      {label}
      <input
        aria-label={label}
        type="number"
        min={min}
        max={max}
        className={inputClass}
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value === '' ? undefined : Number(e.target.value))}
      />
    </label>
  )
}

function MonitorFields({ g, set, err }: { g: Gateway; set: (patch: Partial<Gateway>) => void; err: FormCtx['err'] }) {
  const m = g.monitor ?? {}
  const setMonitor = (patch: Partial<GatewayMonitor>) => set({ monitor: { ...m, ...patch } })
  return (
    <fieldset className="rounded border border-gray-200 p-3 dark:border-gray-700">
      <legend className="px-1 text-sm font-semibold text-gray-800 dark:text-gray-200">{t('gateways.healthMonitor')}</legend>
      <label className="mb-2 flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input type="checkbox" checked={!!m.disabled} onChange={(e) => setMonitor({ disabled: e.target.checked ? true : undefined })} />
        {t('gateways.alwaysUp')}
      </label>
      {!m.disabled && (
        <>
          <label className="block">
            {t('gateways.monitorIp')}
            <input aria-label={t('gateways.monitorIp')} className={inputClass} placeholder={t('gateways.monitorIpPlaceholder')} value={m.ip ?? ''} onChange={(e) => setMonitor({ ip: e.target.value })} />
            {err('monitor.ip')}
          </label>
          <div className="mt-2 grid grid-cols-1 sm:grid-cols-3 gap-2">
            <Num label={t('gateways.interval')} value={m.interval_seconds} onChange={(v) => setMonitor({ interval_seconds: v })} min={1} max={60} />
            <Num label={t('gateways.lossPercent')} value={m.loss_percent} onChange={(v) => setMonitor({ loss_percent: v })} min={0} max={100} />
            <Num label={t('gateways.latencyMs')} value={m.latency_ms} onChange={(v) => setMonitor({ latency_ms: v })} min={0} />
          </div>
          {err('monitor')}
        </>
      )}
    </fieldset>
  )
}

function targetLine(status: TargetStatus) {
  const mode = status.mode === 'blackhole' ? t('gateways.modeBlackhole') : status.mode
  return (
    <span className="text-xs text-gray-600 dark:text-gray-400">
      {t('gateways.targetLine', {
        active: status.active?.length ? status.active.join(', ') : t('gateways.none'),
        mode,
      })}
    </span>
  )
}

// gateways: the page's live list, so one added a moment ago can be the default
function StatusPanel({ cfg, gatewayNames }: { cfg: Candidate; gatewayNames: string[] }) {
  const q = useQuery({ queryKey: ['gateways', 'status'], queryFn: getGatewayStatus, refetchInterval: 5000 })
  const [def, setDef] = useState(cfg.default_gateway ?? '')
  const [defError, setDefError] = useState<string | null>(null)
  const groups = (cfg.gateway_groups ?? []).map((g) => g.name)
  const gateways = gatewayNames
  const [defSaved, setDefSaved] = useState(false)

  const changeDefault = async (v: string) => {
    setDef(v)
    setDefError(null)
    setDefSaved(false)
    try {
      await patchSection('default_gateway', v)
      setDefSaved(true)
    } catch (e) {
      setDefError((e as Error).message)
    }
  }

  return (
    <div className="mt-6">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-800 dark:text-gray-100">{t('gateways.wanStatus')}</h3>
        <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          {t('gateways.defaultRoute')}
          <select
            aria-label={t('gateways.defaultRoute')}
            className="rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600"
            value={def}
            onChange={(e) => changeDefault(e.target.value)}
          >
            <option value="">{t('gateways.defaultAutomatic')}</option>
            {gateways.map((n) => <option key={n} value={n}>{n}</option>)}
            {groups.map((n) => <option key={n} value={n}>{t('gateways.groupOption', { name: n })}</option>)}
          </select>
        </label>
      </div>
      {defError && <div role="alert" className="mb-2 rounded bg-red-50 p-2 text-sm text-red-700">{defError}</div>}
      {defSaved && <p role="status" className="mb-2 text-sm text-green-700 dark:text-green-400">{t('gateways.defaultSaved')}</p>}
      {q.isLoading && <p className="text-sm text-gray-500">{t('gateways.loadingStatus')}</p>}
      {q.isError && <p className="text-sm text-gray-500">{t('gateways.statusUnavailable')}</p>}
      {q.data && (
        <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2">{t('gateways.col.gateway')}</th>
              <th className="px-3 py-2">{t('gateways.col.status')}</th>
              <th className="px-3 py-2">{t('gateways.col.loss')}</th>
              <th className="px-3 py-2">{t('gateways.col.latency')}</th>
            </tr>
          </thead>
          <tbody>
            {(q.data.gateways ?? []).map((s) => (
              <tr key={s.name} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-1">{s.name}</td>
                <td className="px-3 py-1">
                  <Badge variant={s.state === 'up' ? 'success' : s.state === 'down' ? 'error' : 'secondary'}>{s.state}</Badge>
                  {s.reason && <span className="ml-2 text-xs text-gray-500">{s.reason}</span>}
                </td>
                <td className="px-3 py-1 tabular-nums">{s.loss_percent}%</td>
                <td className="px-3 py-1 tabular-nums">{t('gateways.latencyValue', { ms: s.latency_ms })}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {q.data && (
        <ul aria-label={t('gateways.routingTargets')} className="mt-3 space-y-1 text-sm text-gray-700 dark:text-gray-300">
          {q.data.default && (
            <li>
              {t('gateways.defaultRouteLine')} {targetLine(q.data.default)}
            </li>
          )}
          {(q.data.targets ?? []).map((tg) => (
            <li key={tg.name}>
              {tg.group ? t('gateways.groupLabel') : t('gateways.gatewayLabel')} {tg.name}: {targetLine(tg)}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export default function Gateways() {
  return (
    <ConfigList<Gateway>
      title={t('gateways.title')}
      path="gateways"
      newLabel={t('gateways.newGateway')}
      intro={t('gateways.intro')}
      select={(c) => c.gateways}
      empty={() => ({ name: '', interface: '', address: '', monitor: {} })}
      clean={(g) => {
        const out: Gateway = { name: g.name.trim(), interface: g.interface }
        const addr = g.address?.trim()
        if (addr) out.address = addr === 'dhcp' ? 'dhcp' : addr
        const m = g.monitor
        if (m) {
          const mon: GatewayMonitor = {}
          if (m.ip?.trim()) mon.ip = m.ip.trim()
          if (m.disabled) mon.disabled = true
          if (m.interval_seconds) mon.interval_seconds = m.interval_seconds
          if (m.loss_percent) mon.loss_percent = m.loss_percent
          if (m.latency_ms) mon.latency_ms = m.latency_ms
          if (Object.keys(mon).length) out.monitor = mon
        }
        return out
      }}
      validate={(g, cfg) => validateGateway(g, cfg)}
      columns={[
        { label: t('gateways.col.interface'), render: (g) => g.interface },
        { label: t('gateways.col.address'), render: (g) => (g.address ? (g.address === 'dhcp' ? 'DHCP' : g.address) : '—') },
        { label: t('gateways.col.monitor'), render: (g) => (g.monitor?.disabled ? t('gateways.monitorOff') : g.monitor?.ip ?? t('gateways.monitorAuto')) },
      ]}
      after={(cfg, items) => <StatusPanel cfg={cfg} gatewayNames={items.map((g) => g.name)} />}
      form={(g, set, { cfg, err }) => {
        const wans = wanInterfaces(cfg)
        const clients = wireGuardClients(cfg)
        const isTunnel = clients.includes(g.interface)
        return (
          <>
            <label className="block">
              {t('gateways.interface')}
              <select aria-label={t('gateways.interface')} className={inputClass} value={g.interface} onChange={(e) => set({ interface: e.target.value, address: '' })}>
                <option value="">{t('gateways.pickInterface')}</option>
                {wans.map((n) => <option key={n} value={n}>{n}</option>)}
                {clients.map((n) => <option key={n} value={n}>{t('gateways.tunnelOption', { name: n })}</option>)}
              </select>
              {err('interface')}
            </label>
            {isTunnel ? (
              <p className="text-sm text-gray-500">{t('gateways.tunnelNote')}</p>
            ) : (
              <label className="block">
                {t('gateways.address')}
                <input aria-label={t('gateways.address')} className={inputClass} placeholder={t('gateways.addressPlaceholder')} value={g.address ?? ''} onChange={(e) => set({ address: e.target.value })} />
                {err('address')}
              </label>
            )}
            <MonitorFields g={g} set={set} err={err} />
          </>
        )
      }}
    />
  )
}
