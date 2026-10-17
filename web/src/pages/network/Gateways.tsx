// Gateways (config "gateways"): one per WAN interface/VLAN or WireGuard
// tunnel, with a monitor ping and live up/down status.
import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ConfigList, inputClass, type FormCtx } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { patchSection, type Candidate, type Gateway, type GatewayMonitor } from '../../api/objects'
import { getGatewayStatus, type TargetStatus } from '../../api/gateways'
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
      <legend className="px-1 text-sm font-semibold text-gray-800 dark:text-gray-200">Health monitor</legend>
      <label className="mb-2 flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input type="checkbox" checked={!!m.disabled} onChange={(e) => setMonitor({ disabled: e.target.checked ? true : undefined })} />
        Keep this gateway always up (no pings)
      </label>
      {!m.disabled && (
        <>
          <label className="block">
            Monitor IP
            <input aria-label="Monitor IP" className={inputClass} placeholder="auto (public resolver)" value={m.ip ?? ''} onChange={(e) => setMonitor({ ip: e.target.value })} />
            {err('monitor.ip')}
          </label>
          <div className="mt-2 grid grid-cols-3 gap-2">
            <Num label="Interval (s)" value={m.interval_seconds} onChange={(v) => setMonitor({ interval_seconds: v })} min={1} max={60} />
            <Num label="Loss %" value={m.loss_percent} onChange={(v) => setMonitor({ loss_percent: v })} min={0} max={100} />
            <Num label="Latency (ms)" value={m.latency_ms} onChange={(v) => setMonitor({ latency_ms: v })} min={0} />
          </div>
          {err('monitor')}
        </>
      )}
    </fieldset>
  )
}

function targetLine(t: TargetStatus) {
  const mode = t.mode === 'blackhole' ? 'blackhole (kill switch)' : t.mode
  return (
    <span className="text-xs text-gray-600 dark:text-gray-400">
      {t.active?.length ? t.active.join(', ') : '(none)'} · {mode}
    </span>
  )
}

function StatusPanel({ cfg }: { cfg: Candidate }) {
  const q = useQuery({ queryKey: ['gateways', 'status'], queryFn: getGatewayStatus, refetchInterval: 5000 })
  const [def, setDef] = useState(cfg.default_gateway ?? '')
  const [defError, setDefError] = useState<string | null>(null)
  const groups = (cfg.gateway_groups ?? []).map((g) => g.name)
  const gateways = (cfg.gateways ?? []).map((g) => g.name)

  const changeDefault = async (v: string) => {
    setDef(v)
    setDefError(null)
    try {
      await patchSection('default_gateway', v)
    } catch (e) {
      setDefError((e as Error).message)
    }
  }

  return (
    <div className="mt-6">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-800 dark:text-gray-100">WAN status</h3>
        <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          Default route
          <select
            aria-label="Default route"
            className="rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600"
            value={def}
            onChange={(e) => changeDefault(e.target.value)}
          >
            <option value="">automatic (failover in list order)</option>
            {gateways.map((n) => <option key={n} value={n}>{n}</option>)}
            {groups.map((n) => <option key={n} value={n}>group: {n}</option>)}
          </select>
        </label>
      </div>
      {defError && <div role="alert" className="mb-2 rounded bg-red-50 p-2 text-sm text-red-700">{defError}</div>}
      {q.isLoading && <p className="text-sm text-gray-500">Loading status…</p>}
      {q.isError && <p className="text-sm text-gray-500">Gateway status is not available.</p>}
      {q.data && (
        <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2">Gateway</th>
              <th className="px-3 py-2">Status</th>
              <th className="px-3 py-2">Loss</th>
              <th className="px-3 py-2">Latency</th>
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
                <td className="px-3 py-1 tabular-nums">{s.latency_ms} ms</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {q.data && (
        <ul aria-label="Routing targets" className="mt-3 space-y-1 text-sm text-gray-700 dark:text-gray-300">
          {q.data.default && (
            <li>
              Default route: {targetLine(q.data.default)}
            </li>
          )}
          {(q.data.targets ?? []).map((t) => (
            <li key={t.name}>
              {t.group ? 'Group' : 'Gateway'} {t.name}: {targetLine(t)}
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
      title="Gateways"
      path="gateways"
      newLabel="New gateway"
      intro="One gateway per WAN interface or WireGuard tunnel. Once you define gateways, every enabled WAN needs one."
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
        { label: 'Interface', render: (g) => g.interface },
        { label: 'Address', render: (g) => (g.address ? (g.address === 'dhcp' ? 'DHCP' : g.address) : '—') },
        { label: 'Monitor', render: (g) => (g.monitor?.disabled ? 'off' : g.monitor?.ip ?? 'auto') },
      ]}
      after={(cfg) => <StatusPanel cfg={cfg} />}
      form={(g, set, { cfg, err }) => {
        const wans = wanInterfaces(cfg)
        const clients = wireGuardClients(cfg)
        const isTunnel = clients.includes(g.interface)
        return (
          <>
            <label className="block">
              Interface
              <select aria-label="Interface" className={inputClass} value={g.interface} onChange={(e) => set({ interface: e.target.value, address: '' })}>
                <option value="">Pick an interface</option>
                {wans.map((n) => <option key={n} value={n}>{n}</option>)}
                {clients.map((n) => <option key={n} value={n}>{n} (tunnel)</option>)}
              </select>
              {err('interface')}
            </label>
            {isTunnel ? (
              <p className="text-sm text-gray-500">Tunnels have no address; traffic is routed by the provider.</p>
            ) : (
              <label className="block">
                Address
                <input aria-label="Address" className={inputClass} placeholder='IPv4 or "dhcp"' value={g.address ?? ''} onChange={(e) => set({ address: e.target.value })} />
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
