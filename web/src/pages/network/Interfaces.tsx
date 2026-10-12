// Interfaces and VLANs (config "interfaces", "vlans"): role, device and
// addressing. Interfaces and VLANs share one name space.
import { useEffect, useState } from 'react'
import { Checklist, ConfigList, inputClass, omitEmpty, type FormCtx } from '../../components/ConfigList'
import { getDevices, type Device, type Interface, type VLAN } from '../../api/objects'
import { Badge } from '../../components/Badge'
import { validateAddressing } from './networkLogic'

type Addressed = Interface | VLAN

// cleanAddressing keeps address/gateway only for static mode and drops empty
// optional fields (the server parses address and gateway as IPs).
function cleanAddressing<T extends Addressed>(x: T): T {
  const out = { ...x, description: x.description?.trim() }
  if (out.mode !== 'static') {
    delete out.address
    delete out.gateway
  }
  if (!out.mtu) delete out.mtu
  return omitEmpty(out)
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

const addr = (x: Addressed) => (x.mode === 'static' ? x.address : x.mode)
const status = (x: Addressed) => (x.enabled ? <Badge variant="success">enabled</Badge> : <Badge variant="secondary">disabled</Badge>)

export default function Interfaces() {
  const [devices, setDevices] = useState<Device[] | null>(null)
  useEffect(() => {
    getDevices().then(setDevices).catch(() => setDevices([]))
  }, [])
  const missing = (dev: string) => devices !== null && devices.length > 0 && dev !== '' && !devices.some((d) => d.name === dev)

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
          { label: 'Access', render: (x) => (x.allow_access ?? []).join(', ') },
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
        validate={(x, cfg) => ({
          ...validateAddressing(x),
          ...(Number.isInteger(x.vlan_id) && x.vlan_id >= 1 && x.vlan_id <= 4094 ? {} : { vlan_id: 'VLAN ID must be 1-4094' }),
          ...((cfg.interfaces ?? []).some((i) => i.name === x.parent) ? {} : { parent: 'Pick the parent interface' }),
          ...((cfg.interfaces ?? []).some((i) => i.name === x.name) ? { name: 'An interface has this name' } : {}),
        })}
        columns={[
          { label: 'Parent', render: (x) => x.parent },
          { label: 'VLAN ID', render: (x) => x.vlan_id },
          { label: 'Address', render: addr },
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
          </>
        )}
      />
    </div>
  )
}
