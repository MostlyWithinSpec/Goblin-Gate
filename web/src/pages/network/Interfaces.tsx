// Interfaces and VLANs (config "interfaces", "vlans"): role, device and
// addressing. Interfaces and VLANs share one name space.
import { useEffect, useState } from 'react'
import { Checklist, ConfigList, inputClass, omitEmpty, type FormCtx } from '../../components/ConfigList'
import { getDevices, type Device, type Interface, type IPv6, type SQM, type VLAN } from '../../api/objects'
import { getSystemStatus, type SQMStatus } from '../../api/config'
import { Badge } from '../../components/Badge'
import { validateAddressing, validateIPv6, validateSQM } from './networkLogic'

type Addressed = Interface | VLAN

// cleanAddressing keeps address/gateway only for static mode, cleans the ipv6
// block per its mode, and drops empty optional fields.
function cleanAddressing<T extends Addressed>(x: T): T {
  const out = { ...x, description: x.description?.trim() }
  if (out.mode !== 'static') {
    delete out.address
    delete out.gateway
  }
  if (!out.mtu) delete out.mtu
  // Clean the ipv6 block per mode.
  if (out.ipv6 && out.ipv6.mode !== 'none') {
    const v = { ...out.ipv6 }
    if (v.mode === 'static') {
      delete v.pd_size
      delete v.uplink
      delete v.prefix_id
      if (!v.address) delete v.address
      if (!v.gateway) delete v.gateway
    } else if (v.mode === 'dhcp6') {
      delete v.address
      delete v.gateway
      delete v.uplink
      delete v.prefix_id
      delete v.ra
    } else if (v.mode === 'delegated') {
      delete v.address
      delete v.gateway
      delete v.pd_size
      if (v.uplink === '') delete v.uplink
      if (v.prefix_id === undefined || v.prefix_id === null) delete v.prefix_id
    }
    // If ra is default (enabled+dns), omit it to keep the server default.
    if (v.ra && v.ra.enabled === true && v.ra.dns === true && !v.ra.managed && !v.ra.other) {
      delete v.ra
    }
    out.ipv6 = v
  } else if (out.ipv6?.mode === 'none') {
    delete out.ipv6
  }
  // Smart queue: keep the speeds even when disabled so re-enabling does not
  // require typing them again. An enabled block without speeds must survive
  // clean so validateSQM can report the missing values; only drop a block
  // that is disabled and has no speeds at all.
  if ('sqm' in out) {
    const s = (out as Interface).sqm
    if (!s || (s.enabled !== true && s.download_mbit === undefined && s.upload_mbit === undefined)) delete (out as Interface).sqm
    else (out as Interface).sqm = { enabled: !!s.enabled, download_mbit: s.download_mbit, upload_mbit: s.upload_mbit }
  }
  return omitEmpty(out)
}

// roleOf returns the effective role for IPv6 validation: for a VLAN, use the
// parent interface's role.
function roleOf(x: Addressed, cfg: { interfaces?: Interface[]; vlans?: VLAN[] }): string {
  if ('device' in x && (x as Interface).role) return (x as Interface).role
  // VLAN: look up parent
  const parent = (cfg.interfaces ?? []).find((i) => i.name === (x as VLAN).parent)
  return parent?.role ?? 'lan'
}

function IPv6Fields<T extends Addressed>({ x, set, ctx }: { x: T; set: (p: Partial<T>) => void; ctx: FormCtx }) {
  const put = (p: Partial<Addressed>) => set(p as Partial<T>)
  const v: IPv6 = x.ipv6 ?? { mode: 'none' }
  const role = roleOf(x, ctx.cfg)
  const wan = role === 'wan'

  const setV6 = (p: Partial<IPv6>) => put({ ipv6: { ...v, ...p } })

  // Uplink options: interfaces/VLANs with dhcp6 mode
  const uplinks = [
    ...(ctx.cfg.interfaces ?? []).filter((i) => i.ipv6?.mode === 'dhcp6').map((i) => i.name),
    ...(ctx.cfg.vlans ?? []).filter((vl) => vl.ipv6?.mode === 'dhcp6').map((vl) => vl.name),
  ]

  return (
    <div className="space-y-3">
      <h4 className="text-sm font-semibold">IPv6</h4>
      <label className="block">
        IPv6 mode
        <select aria-label="IPv6 mode" className={inputClass} value={v.mode} onChange={(e) => setV6({ mode: e.target.value as IPv6['mode'] })}>
          <option value="none">None</option>
          {wan && <option value="dhcp6">DHCPv6 client</option>}
          {!wan && <option value="delegated">Delegated (from uplink)</option>}
          <option value="static">Static</option>
        </select>
      </label>
      {ctx.err('mode')}

      {v.mode === 'static' && (
        <>
          <label className="block">
            Address
            <input aria-label="IPv6 address" className={inputClass} placeholder="2001:db8::1/64" value={v.address ?? ''} onChange={(e) => setV6({ address: e.target.value.trim() })} />
            {ctx.err('ipv6_address')}
          </label>
          {wan && (
            <label className="block">
              Gateway
              <input aria-label="IPv6 gateway" className={inputClass} placeholder="2001:db8::1" value={v.gateway ?? ''} onChange={(e) => setV6({ gateway: e.target.value.trim() })} />
              {ctx.err('ipv6_gateway')}
            </label>
          )}
        </>
      )}

      {v.mode === 'dhcp6' && (
        <label className="block">
          Prefix delegation size
          <input aria-label="IPv6 PD size" type="number" min={48} max={64} className={inputClass} placeholder="56" value={v.pd_size ?? ''} onChange={(e) => setV6({ pd_size: e.target.value ? Number(e.target.value) : undefined })} />
          {ctx.err('ipv6_pd_size')}
        </label>
      )}

      {v.mode === 'delegated' && (
        <>
          <label className="block">
            Uplink
            <select aria-label="IPv6 uplink" className={inputClass} value={v.uplink ?? ''} onChange={(e) => setV6({ uplink: e.target.value })}>
              <option value="">Automatic</option>
              {uplinks.map((u) => (
                <option key={u} value={u}>{u}</option>
              ))}
            </select>
            {ctx.err('ipv6_uplink')}
          </label>
          <label className="block">
            Prefix ID
            <input aria-label="IPv6 prefix ID" type="number" min={0} className={inputClass} placeholder="auto" value={v.prefix_id ?? ''} onChange={(e) => setV6({ prefix_id: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('ipv6_prefix_id')}
          </label>
        </>
      )}

      {(v.mode === 'static' || v.mode === 'delegated') && (
        <>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={v.ra?.enabled ?? true} onChange={(e) => setV6({ ra: { enabled: e.target.checked, dns: v.ra?.dns ?? true } })} />
            Router advertisements
          </label>
          {(v.ra?.enabled ?? true) && (
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={v.ra?.dns ?? true} onChange={(e) => setV6({ ra: { enabled: true, dns: e.target.checked } })} />
              Announce DNS
            </label>
          )}
        </>
      )}
    </div>
  )
}

function AddressingFields<T extends Addressed>({ x, set, ctx }: { x: T; set: (p: Partial<T>) => void; ctx: FormCtx }) {
  const put = (p: Partial<Addressed>) => set(p as Partial<T>)
  return (
    <>
      <label className="block">
        Mode
        <select aria-label="Mode" className={inputClass} value={x.mode} onChange={(e) => put({ mode: e.target.value })}>
          <option value="static">Static</option>
          <option value="dhcp">DHCP client</option>
          <option value="none">None</option>
        </select>
      </label>
      {x.mode === 'static' && (
        <>
          <label className="block">
            Address
            <input aria-label="Address" className={inputClass} placeholder="192.168.1.1/24" value={x.address ?? ''} onChange={(e) => put({ address: e.target.value.trim() })} />
            {ctx.err('address')}
          </label>
          <label className="block">
            Gateway
            <input aria-label="Gateway" className={inputClass} placeholder="only for an upstream link" value={x.gateway ?? ''} onChange={(e) => put({ gateway: e.target.value.trim() })} />
            {ctx.err('gateway')}
          </label>
        </>
      )}
      <label className="block">
        MTU
        <input aria-label="MTU" type="number" className={inputClass} placeholder="1500" value={x.mtu ?? ''} onChange={(e) => put({ mtu: e.target.value ? Number(e.target.value) : undefined })} />
        {ctx.err('mtu')}
      </label>
      <Checklist label="Allow access to the box" options={['ping', 'https', 'ssh']} value={x.allow_access ?? []} onChange={(allow_access) => put({ allow_access })} />
      <label className="block">
        Description
        <input aria-label="Description" className={inputClass} value={x.description ?? ''} onChange={(e) => put({ description: e.target.value })} />
      </label>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={x.enabled} onChange={(e) => put({ enabled: e.target.checked })} /> Enabled
      </label>
    </>
  )
}

// SQMFields renders the smart queue section of a WAN interface form.
function SQMFields({ x, set, ctx }: { x: Interface; set: (p: Partial<Interface>) => void; ctx: FormCtx }) {
  const s: SQM = x.sqm ?? { enabled: false }
  const setS = (p: Partial<SQM>) => set({ sqm: { ...s, ...p } })
  return (
    <div className="space-y-3 rounded border p-3">
      <h4 className="text-sm font-semibold">Smart queue (reduces lag when the line is busy)</h4>
      <label className="flex items-center gap-2">
        <input type="checkbox" aria-label="Enable smart queue" checked={s.enabled} onChange={(e) => setS({ enabled: e.target.checked })} />
        Enable smart queue
      </label>
      {ctx.err('sqm')}
      {s.enabled && (
        <>
          <label className="block">
            Download speed (Mbit/s)
            <input aria-label="SQM download Mbit" type="number" min={1} max={100000} className={inputClass} placeholder="943" value={s.download_mbit ?? ''} onChange={(e) => setS({ download_mbit: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('sqm_download')}
          </label>
          <label className="block">
            Upload speed (Mbit/s)
            <input aria-label="SQM upload Mbit" type="number" min={1} max={100000} className={inputClass} placeholder="94" value={s.upload_mbit ?? ''} onChange={(e) => setS({ upload_mbit: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('sqm_upload')}
          </label>
          <p className="text-xs opacity-70">Run a speed test without smart queue, then enter about 90% of the result.</p>
        </>
      )}
    </div>
  )
}

const addr = (x: Addressed) => (x.mode === 'static' ? x.address : x.mode)
const v6addr = (x: Addressed) => {
  if (!x.ipv6 || x.ipv6.mode === 'none') return ''
  if (x.ipv6.mode === 'static') return x.ipv6.address ?? ''
  return x.ipv6.mode
}
const status = (x: Addressed) => (x.enabled ? <Badge variant="success">enabled</Badge> : <Badge variant="secondary">disabled</Badge>)

export default function Interfaces() {
  const [devices, setDevices] = useState<Device[] | null>(null)
  const [sqmStatus, setSqmStatus] = useState<SQMStatus[]>([])
  useEffect(() => {
    getDevices().then(setDevices).catch(() => setDevices([]))
    getSystemStatus()
      .then((s) => setSqmStatus(s.sqm ?? []))
      .catch(() => setSqmStatus([]))
  }, [])
  const missing = (dev: string) => devices !== null && devices.length > 0 && dev !== '' && !devices.some((d) => d.name === dev)
  // SQM badge for WANs with smart queue enabled; the tooltip says whether CAKE
  // is actually running on the device.
  const sqmBadge = (x: Interface) => {
    if (!x.sqm?.enabled) return null
    const live = sqmStatus.find((s) => s.interface === x.name)
    const active = live ? (live.active ? 'active' : 'not active') : 'unknown'
    return <span title={`smart queue ${active}`}><Badge variant="secondary">SQM</Badge></span>
  }

  return (
    <div className="space-y-8">
      <ConfigList<Interface>
        title="Interfaces"
        path="interfaces"
        newLabel="New interface"
        select={(c) => c.interfaces}
        empty={() => ({ name: '', device: '', role: 'lan', mode: 'static', enabled: true })}
        clean={cleanAddressing}
        validate={(x, cfg) => ({
          ...validateAddressing(x),
          ...validateIPv6(x.ipv6, x.role, cfg),
          ...validateSQM(x.sqm, x.role),
          ...(x.device ? {} : { device: 'Pick the network device' }),
          ...((cfg.vlans ?? []).some((v) => v.name === x.name) ? { name: 'A VLAN has this name' } : {}),
        })}
        columns={[
          {
            label: 'Device',
            render: (x) => (
              <>
                <code>{x.device}</code>
                {missing(x.device) && <span className="ml-1 text-red-600">(missing)</span>}
              </>
            ),
          },
          { label: 'Role', render: (x) => x.role },
          { label: 'Address', render: addr },
          { label: 'IPv6', render: v6addr },
          { label: 'Access', render: (x) => (x.allow_access ?? []).join(', ') },
          { label: 'SQM', render: sqmBadge },
          { label: 'Status', render: status },
        ]}
        form={(x, set, ctx) => (
          <>
            <label className="block">
              Device
              {devices && devices.length > 0 ? (
                <select aria-label="Device" className={inputClass} value={x.device} onChange={(e) => set({ device: e.target.value })}>
                  <option value="">—</option>
                  {devices.map((d) => (
                    <option key={d.name} value={d.name}>
                      {d.name}
                      {d.mac ? ` (${d.mac})` : ''}
                      {d.up ? '' : ' down'}
                    </option>
                  ))}
                  {missing(x.device) && <option value={x.device}>{x.device} (missing)</option>}
                </select>
              ) : (
                <input aria-label="Device" className={inputClass} placeholder="eth0" value={x.device} onChange={(e) => set({ device: e.target.value.trim() })} />
              )}
              {ctx.err('device')}
            </label>
            <label className="block">
              Role
              <select aria-label="Role" className={inputClass} value={x.role} onChange={(e) => set({ role: e.target.value })}>
                <option value="wan">WAN</option>
                <option value="lan">LAN</option>
                <option value="dmz">DMZ</option>
                <option value="guest">Guest</option>
                <option value="mgmt">Management</option>
              </select>
            </label>
            <AddressingFields x={x} set={set} ctx={ctx} />
            {x.role === 'wan' && <SQMFields x={x} set={set} ctx={ctx} />}
            <IPv6Fields x={x} set={set} ctx={ctx} />
          </>
        )}
      />

      <ConfigList<VLAN>
        title="VLANs"
        path="vlans"
        newLabel="New VLAN"
        select={(c) => c.vlans}
        empty={(c) => ({ name: '', parent: c.interfaces?.[0]?.name ?? '', vlan_id: 10, mode: 'static', enabled: true })}
        clean={cleanAddressing}
        validate={(x, cfg) => {
          const role = (cfg.interfaces ?? []).find((i) => i.name === x.parent)?.role ?? 'lan'
          return {
            ...validateAddressing(x),
            ...validateIPv6(x.ipv6, role, cfg),
            ...(Number.isInteger(x.vlan_id) && x.vlan_id >= 1 && x.vlan_id <= 4094 ? {} : { vlan_id: 'VLAN ID must be 1-4094' }),
            ...((cfg.interfaces ?? []).some((i) => i.name === x.parent) ? {} : { parent: 'Pick the parent interface' }),
            ...((cfg.interfaces ?? []).some((i) => i.name === x.name) ? { name: 'An interface has this name' } : {}),
          }
        }}
        columns={[
          { label: 'Parent', render: (x) => x.parent },
          { label: 'VLAN ID', render: (x) => x.vlan_id },
          { label: 'Address', render: addr },
          { label: 'IPv6', render: v6addr },
          { label: 'Status', render: status },
        ]}
        form={(x, set, ctx) => (
          <>
            <label className="block">
              Parent interface
              <select aria-label="Parent interface" className={inputClass} value={x.parent} onChange={(e) => set({ parent: e.target.value })}>
                {(ctx.cfg.interfaces ?? []).map((i) => <option key={i.name} value={i.name}>{i.name}</option>)}
              </select>
              {ctx.err('parent')}
            </label>
            <label className="block">
              VLAN ID
              <input aria-label="VLAN ID" type="number" className={inputClass} value={x.vlan_id} onChange={(e) => set({ vlan_id: Number(e.target.value) })} />
              {ctx.err('vlan_id')}
            </label>
            <AddressingFields x={x} set={set} ctx={ctx} />
            <IPv6Fields x={x} set={set} ctx={ctx} />
          </>
        )}
      />
    </div>
  )
}
