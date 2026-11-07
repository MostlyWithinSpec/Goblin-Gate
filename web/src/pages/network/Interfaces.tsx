// Interfaces and VLANs (config "interfaces", "vlans"): role, device and
// addressing. Interfaces and VLANs share one name space.
import { useEffect, useState } from 'react'
import { Checklist, ConfigList, inputClass, omitEmpty, type FormCtx } from '../../components/ConfigList'
import { getDevices, type Device, type Interface, type IPv6, type Offloads, type SQM, type VLAN } from '../../api/objects'
import { getSystemStatus, type SQMStatus } from '../../api/config'
import { Badge } from '../../components/Badge'
import { validateAddressing, validateIPv6, validateSQM } from './networkLogic'
import { Dialog } from '../../components/Dialog'
import { t } from '../../i18n'

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
  // Offloads: a field set to the driver default is left out, and so is an
  // object with nothing left in it.
  if ('offloads' in out) {
    const o = Object.fromEntries(Object.entries((out as Interface).offloads ?? {}).filter(([, v]) => typeof v === 'boolean'))
    if (Object.keys(o).length === 0) delete (out as Interface).offloads
    else (out as Interface).offloads = o as Offloads
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
      <h4 className="text-sm font-semibold">{t('ipv6.title')}</h4>
      <label className="block">
        {t('ipv6.mode')}
        <select aria-label={t('ipv6.mode')} className={inputClass} value={v.mode} onChange={(e) => setV6({ mode: e.target.value as IPv6['mode'] })}>
          <option value="none">{t('ipv6.modeNone')}</option>
          {wan && <option value="dhcp6">{t('ipv6.dhcp6')}</option>}
          {!wan && <option value="delegated">{t('ipv6.delegated')}</option>}
          <option value="static">{t('interfaces.mode.static')}</option>
        </select>
      </label>
      {ctx.err('mode')}

      {v.mode === 'static' && (
        <>
          <label className="block">
            {t('ipv6.address')}
            <input aria-label={t('ipv6.addressAria')} className={inputClass} placeholder="2001:db8::1/64" value={v.address ?? ''} onChange={(e) => setV6({ address: e.target.value.trim() })} />
            {ctx.err('ipv6_address')}
          </label>
          {wan && (
            <label className="block">
              {t('ipv6.gateway')}
              <input aria-label={t('ipv6.gatewayAria')} className={inputClass} placeholder="2001:db8::1" value={v.gateway ?? ''} onChange={(e) => setV6({ gateway: e.target.value.trim() })} />
              {ctx.err('ipv6_gateway')}
            </label>
          )}
        </>
      )}

      {v.mode === 'dhcp6' && (
        <label className="block">
          {t('ipv6.pdSize')}
          <input aria-label={t('ipv6.pdAria')} type="number" min={48} max={64} className={inputClass} placeholder="56" value={v.pd_size ?? ''} onChange={(e) => setV6({ pd_size: e.target.value ? Number(e.target.value) : undefined })} />
          {ctx.err('ipv6_pd_size')}
        </label>
      )}

      {v.mode === 'delegated' && (
        <>
          <label className="block">
            {t('ipv6.uplink')}
            <select aria-label={t('ipv6.uplinkAria')} className={inputClass} value={v.uplink ?? ''} onChange={(e) => setV6({ uplink: e.target.value })}>
              <option value="">{t('ipv6.automatic')}</option>
              {uplinks.map((u) => (
                <option key={u} value={u}>{u}</option>
              ))}
            </select>
            {ctx.err('ipv6_uplink')}
          </label>
          <label className="block">
            {t('ipv6.prefixId')}
            <input aria-label={t('ipv6.prefixIdAria')} type="number" min={0} className={inputClass} placeholder="auto" value={v.prefix_id ?? ''} onChange={(e) => setV6({ prefix_id: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('ipv6_prefix_id')}
          </label>
        </>
      )}

      {(v.mode === 'static' || v.mode === 'delegated') && (
        <>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={v.ra?.enabled ?? true} onChange={(e) => setV6({ ra: { enabled: e.target.checked, dns: v.ra?.dns ?? true } })} />
            {t('ipv6.ra')}
          </label>
          {(v.ra?.enabled ?? true) && (
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={v.ra?.dns ?? true} onChange={(e) => setV6({ ra: { enabled: true, dns: e.target.checked } })} />
              {t('ipv6.announceDns')}
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
        {t('interfaces.mode')}
        <select aria-label={t('interfaces.mode')} className={inputClass} value={x.mode} onChange={(e) => put({ mode: e.target.value })}>
          <option value="static">{t('interfaces.mode.static')}</option>
          <option value="dhcp">{t('interfaces.mode.dhcpClient')}</option>
          <option value="none">{t('ipv6.modeNone')}</option>
        </select>
      </label>
      {x.mode === 'static' && (
        <>
          <label className="block">
            {t('interfaces.address')}
            <input aria-label={t('interfaces.address')} className={inputClass} placeholder="192.168.1.1/24" value={x.address ?? ''} onChange={(e) => put({ address: e.target.value.trim() })} />
            {ctx.err('address')}
          </label>
          <label className="block">
            {t('interfaces.gateway')}
            <input aria-label={t('interfaces.gateway')} className={inputClass} placeholder={t('interfaces.gatewayPlaceholder')} value={x.gateway ?? ''} onChange={(e) => put({ gateway: e.target.value.trim() })} />
            {ctx.err('gateway')}
          </label>
        </>
      )}
      <label className="block">
        {t('interfaces.mtu')}
        <input aria-label={t('interfaces.mtu')} type="number" className={inputClass} placeholder="1500" value={x.mtu ?? ''} onChange={(e) => put({ mtu: e.target.value ? Number(e.target.value) : undefined })} />
        {ctx.err('mtu')}
      </label>
      <Checklist label={t('interfaces.access')} options={['ping', 'https', 'ssh']} value={x.allow_access ?? []} onChange={(allow_access) => put({ allow_access })} />
      <label className="block">
        {t('interfaces.description')}
        <input aria-label={t('interfaces.description')} className={inputClass} value={x.description ?? ''} onChange={(e) => put({ description: e.target.value })} />
      </label>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={x.enabled} onChange={(e) => put({ enabled: e.target.checked })} /> {t('interfaces.enabled')}
      </label>
    </>
  )
}

// SQMFields renders the smart queue section of a WAN interface form.
function SQMFields({ x, set, ctx }: { x: Interface; set: (p: Partial<Interface>) => void; ctx: FormCtx }) {
  const s: SQM = x.sqm ?? { enabled: false }
  const setS = (p: Partial<SQM>) => set({ sqm: { ...s, ...p } })
  return (
    <div className="space-y-3 rounded border border-gray-200 p-3 dark:border-gray-600">
      <h4 className="text-sm font-semibold">{t('interfaces.sqmTitle')}</h4>
      <label className="flex items-center gap-2">
        <input type="checkbox" aria-label={t('interfaces.sqmEnable')} checked={s.enabled} onChange={(e) => setS({ enabled: e.target.checked })} />
        {t('interfaces.sqmEnable')}
      </label>
      {ctx.err('sqm')}
      {s.enabled && (
        <>
          <label className="block">
            {t('interfaces.sqmDownload')}
            <input aria-label={t('interfaces.sqmDownloadAria')} type="number" min={1} max={100000} className={inputClass} placeholder="943" value={s.download_mbit ?? ''} onChange={(e) => setS({ download_mbit: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('sqm_download')}
          </label>
          <label className="block">
            {t('interfaces.sqmUpload')}
            <input aria-label={t('interfaces.sqmUploadAria')} type="number" min={1} max={100000} className={inputClass} placeholder="94" value={s.upload_mbit ?? ''} onChange={(e) => setS({ upload_mbit: e.target.value ? Number(e.target.value) : undefined })} />
            {ctx.err('sqm_upload')}
          </label>
          <p className="text-xs opacity-70">{t('interfaces.sqmNote')}</p>
        </>
      )}
    </div>
  )
}

const offloadKeys: [keyof Offloads, string][] = [
  ['gro', 'GRO'],
  ['gso', 'GSO'],
  ['tso', 'TSO'],
  ['lro', 'LRO'],
  ['rx_checksum', 'RX checksum'],
  ['tx_checksum', 'TX checksum'],
]

// OffloadFields: network card offload overrides. Hidden until the admin has
// read the warning, unless an override is already set.
function OffloadFields({ x, set }: { x: Interface; set: (p: Partial<Interface>) => void }) {
  const o: Offloads = x.offloads ?? {}
  const anySet = Object.values(o).some((v) => typeof v === 'boolean')
  const [shown, setShown] = useState(anySet)
  const [asking, setAsking] = useState(false)
  const put = (k: keyof Offloads, v: string) => {
    const next: Offloads = { ...o }
    if (v === '') delete next[k]
    else next[k] = v === 'on'
    set({ offloads: next })
  }
  return (
    <div className="space-y-3 rounded border border-gray-200 p-3 dark:border-gray-600">
      <h4 className="text-sm font-semibold">{t('offloads.title')}</h4>
      {!shown ? (
        <button type="button" className="text-sm text-blue-600 hover:underline dark:text-blue-400" onClick={() => setAsking(true)}>
          {t('offloads.change')}
        </button>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            {offloadKeys.map(([k, label]) => (
              <label key={k} className="block text-sm">
                {label}
                <select aria-label={t('offloads.aria', { name: label })} className={inputClass} value={o[k] === undefined ? '' : o[k] ? 'on' : 'off'} onChange={(e) => put(k, e.target.value)}>
                  <option value="">{t('offloads.default')}</option>
                  <option value="on">{t('offloads.on')}</option>
                  <option value="off">{t('offloads.off')}</option>
                </select>
              </label>
            ))}
          </div>
          <p className="text-xs opacity-70">{t('offloads.note')}</p>
        </>
      )}
      <Dialog open={asking} onClose={() => setAsking(false)} title={t('offloads.warnTitle')} size="small">
        <div className="space-y-3 text-sm">
          <p>{t('offloads.warn')}</p>
          <div className="flex justify-end gap-2">
            <button type="button" className="rounded px-3 py-1.5 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={() => setAsking(false)}>{t('common.cancel')}</button>
            <button type="button" className="rounded bg-amber-600 px-3 py-1.5 text-white hover:bg-amber-700" onClick={() => { setAsking(false); setShown(true) }}>{t('offloads.understood')}</button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}

const addr = (x: Addressed) => (x.mode === 'static' ? x.address : x.mode)
const v6addr = (x: Addressed) => {
  if (!x.ipv6 || x.ipv6.mode === 'none') return ''
  if (x.ipv6.mode === 'static') return x.ipv6.address ?? ''
  return x.ipv6.mode
}
const status = (x: Addressed) => (x.enabled ? <Badge variant="success">{t('interfaces.state.enabled')}</Badge> : <Badge variant="secondary">{t('interfaces.state.disabled')}</Badge>)

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
    const active = live ? (live.active ? t('interfaces.sqmActive') : t('interfaces.sqmInactive')) : t('common.unknown')
    return <span title={t('interfaces.sqmBadge', { state: active })}><Badge variant="secondary">SQM</Badge></span>
  }

  return (
    <div className="space-y-8">
      <ConfigList<Interface>
        title={t('interfaces.title')}
        path="interfaces"
        newLabel={t('interfaces.newInterface')}
        select={(c) => c.interfaces}
        empty={() => ({ name: '', device: '', role: 'lan', mode: 'static', enabled: true })}
        clean={cleanAddressing}
        validate={(x, cfg) => ({
          ...validateAddressing(x),
          ...validateIPv6(x.ipv6, x.role, cfg),
          ...validateSQM(x.sqm, x.role),
          ...(x.device ? {} : { device: t('interfaces.pickDevice') }),
          ...((cfg.vlans ?? []).some((v) => v.name === x.name) ? { name: t('interfaces.vlanNameTaken') } : {}),
        })}
        columns={[
          {
            label: t('interfaces.col.device'),
            render: (x) => (
              <>
                <code>{x.device}</code>
                {missing(x.device) && <span className="ml-1 text-red-600">{t('interfaces.deviceMissing')}</span>}
              </>
            ),
          },
          { label: t('interfaces.col.role'), render: (x) => x.role },
          { label: t('interfaces.col.address'), render: addr },
          { label: t('interfaces.col.ipv6'), render: v6addr },
          { label: t('interfaces.col.access'), render: (x) => (x.allow_access ?? []).join(', ') },
          { label: t('interfaces.col.sqm'), render: sqmBadge },
          { label: t('interfaces.col.status'), render: status },
        ]}
        form={(x, set, ctx) => (
          <>
            <label className="block">
              {t('interfaces.col.device')}
              {devices && devices.length > 0 ? (
                <select aria-label={t('interfaces.deviceAria')} className={inputClass} value={x.device} onChange={(e) => set({ device: e.target.value })}>
                  <option value="">—</option>
                  {devices.map((d) => (
                    <option key={d.name} value={d.name}>
                      {d.name}
                      {d.mac ? ` (${d.mac})` : ''}
                      {d.up ? '' : t('interfaces.downSuffix')}
                    </option>
                  ))}
                  {missing(x.device) && <option value={x.device}>{t('interfaces.deviceMissingName', { name: x.device })}</option>}
                </select>
              ) : (
                <input aria-label={t('interfaces.deviceAria')} className={inputClass} placeholder="eth0" value={x.device} onChange={(e) => set({ device: e.target.value.trim() })} />
              )}
              {ctx.err('device')}
            </label>
            <label className="block">
              {t('interfaces.col.role')}
              <select aria-label={t('interfaces.col.role')} className={inputClass} value={x.role} onChange={(e) => set({ role: e.target.value })}>
                <option value="wan">{t('interfaces.role.wan')}</option>
                <option value="lan">{t('interfaces.role.lan')}</option>
                <option value="dmz">DMZ</option>
                <option value="guest">{t('interfaces.role.guest')}</option>
                <option value="mgmt">{t('interfaces.role.mgmt')}</option>
              </select>
            </label>
            <AddressingFields x={x} set={set} ctx={ctx} />
            {x.role === 'wan' && <SQMFields x={x} set={set} ctx={ctx} />}
            <IPv6Fields x={x} set={set} ctx={ctx} />
            <OffloadFields x={x} set={set} />
          </>
        )}
      />

      <ConfigList<VLAN>
        title={t('vlans.title')}
        path="vlans"
        newLabel={t('interfaces.newVlan')}
        select={(c) => c.vlans}
        empty={(c) => ({ name: '', parent: c.interfaces?.[0]?.name ?? '', vlan_id: 10, mode: 'static', enabled: true })}
        clean={cleanAddressing}
        validate={(x, cfg) => {
          const role = (cfg.interfaces ?? []).find((i) => i.name === x.parent)?.role ?? 'lan'
          return {
            ...validateAddressing(x),
            ...validateIPv6(x.ipv6, role, cfg),
            ...(Number.isInteger(x.vlan_id) && x.vlan_id >= 1 && x.vlan_id <= 4094 ? {} : { vlan_id: t('interfaces.vlanRange') }),
            ...((cfg.interfaces ?? []).some((i) => i.name === x.parent) ? {} : { parent: t('interfaces.pickParent') }),
            ...((cfg.interfaces ?? []).some((i) => i.name === x.name) ? { name: t('interfaces.nameTaken') } : {}),
          }
        }}
        columns={[
          { label: t('vlans.col.parent'), render: (x) => x.parent },
          { label: t('vlans.col.vlanId'), render: (x) => x.vlan_id },
          { label: t('vlans.col.address'), render: addr },
          { label: t('vlans.col.ipv6'), render: v6addr },
          { label: t('vlans.col.status'), render: status },
        ]}
        form={(x, set, ctx) => (
          <>
            <label className="block">
              {t('vlans.parent')}
              <select aria-label={t('vlans.parent')} className={inputClass} value={x.parent} onChange={(e) => set({ parent: e.target.value })}>
                {(ctx.cfg.interfaces ?? []).map((i) => <option key={i.name} value={i.name}>{i.name}</option>)}
              </select>
              {ctx.err('parent')}
            </label>
            <label className="block">
              {t('vlans.vlanId')}
              <input aria-label={t('vlans.vlanId')} type="number" className={inputClass} value={x.vlan_id} onChange={(e) => set({ vlan_id: Number(e.target.value) })} />
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
