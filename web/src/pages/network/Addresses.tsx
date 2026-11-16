// Address objects (config "addresses"): subnet, host, range, FQDN, country or
// live, named once and used by policies and address groups. Country values are
// a comma-joined list of ISO 3166-1 alpha-2 codes picked in the form; a live
// object holds no addresses of its own — its value is the plugin that keeps
// them up to date, and its members come from the live-object API.
import { useEffect, useState } from 'react'
import { ConfigList, inputClass, omitEmpty, type Column } from '../../components/ConfigList'
import { CountryPicker, GeoIPStatusCard, flagEmoji } from '../../components/GeoIP'
import { LiveObjectCell, LivePluginField, pluginsNeedingObjects as pluginsOf, useLiveObjects } from '../../components/LiveObjects'
import type { Address } from '../../api/objects'
import { getPlugins, type PluginStatus } from '../../api/plugins'
import { validateAddress, validateLiveAddress } from './networkLogic'
import { t } from '../../i18n'

const placeholders: Record<Exclude<Address['type'], 'country' | 'live'>, string> = {
  subnet: '192.168.1.0/24 or 2001:db8::/32',
  host: '192.168.1.10 or 2001:db8::1',
  range: '192.168.1.100-192.168.1.150 or 2001:db8::1-2001:db8::ff',
  fqdn: 'example.com',
}

export default function Addresses() {
  const live = useLiveObjects()
  const [installed, setInstalled] = useState<PluginStatus[]>([])
  useEffect(() => {
    getPlugins()
      .then((r) => setInstalled(r.plugins ?? []))
      .catch(() => setInstalled([]))
  }, [])
  const columns: Column<Address>[] = [
    { label: t('addresses.col.type'), render: (a) => (a.type === 'live' ? t('addresses.type.live') : a.type) },
    {
      label: t('addresses.col.value'),
      render: (a) =>
        a.type === 'country' ? (
          <span className="flex flex-wrap gap-1">
            {(a.value ?? '').split(',').filter(Boolean).map((code) => (
              <span key={code} className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-xs dark:bg-gray-700">
                {flagEmoji(code)} {code}
              </span>
            ))}
          </span>
        ) : a.type === 'live' ? (
          <LiveObjectCell address={a} objects={live} />
        ) : (
          <code>{a.value}</code>
        ),
    },
    { label: t('addresses.col.comment'), render: (a) => a.comment },
  ]
  return (
    <ConfigList<Address>
      title={t('addresses.title')}
      path="addresses"
      newLabel={t('addresses.newAddress')}
      select={(c) => c.addresses}
      empty={() => ({ name: '', type: 'subnet', value: '' })}
      clean={(a) => ({ ...omitEmpty({ name: a.name, type: a.type, comment: a.comment?.trim() || undefined }), value: (a.value ?? '').trim() })}
      validate={(a, cfg) =>
        a.type === 'live'
          ? validateLiveAddress(a, (cfg.addresses ?? []) as Address[], pluginsOf(cfg, installed))
          : validateAddress(a)
      }
      // Network → Devices "Create address object" links here with ?new_host=<ip>
      fromURL={(params) => {
        const ip = params.get('new_host')?.trim()
        return ip ? { name: `host_${ip.replace(/[^A-Za-z0-9_.-]/g, '_')}`, type: ip.includes('/') ? 'subnet' : 'host', value: ip } : null
      }}
      intro={<GeoIPStatusCard />}
      columns={columns}
      form={(a, set, { err, cfg }) => (
        <>
          <label className="block">
            {t('addresses.type')}
            <select aria-label={t('addresses.type')} className={inputClass} value={a.type} onChange={(e) => set({ type: e.target.value as Address['type'] })}>
              <option value="subnet">{t('addresses.type.subnet')}</option>
              <option value="host">{t('addresses.type.host')}</option>
              <option value="range">{t('addresses.type.ipRange')}</option>
              <option value="fqdn">{t('addresses.type.fqdn')}</option>
              <option value="country">{t('addresses.type.country')}</option>
              <option value="live">{t('addresses.type.live')}</option>
            </select>
          </label>
          {a.type === 'live' ? (
            <LivePluginField value={a.value} cfg={cfg} installed={installed} onChange={(v) => set({ value: v })} err={err} />
          ) : a.type === 'country' ? (
            <div className="block">
              {t('addresses.countries')}
              <CountryPicker value={a.value} onChange={(v) => set({ value: v })} />
              {err('value')}
            </div>
          ) : (
            <label className="block">
              {t('addresses.value')}
              <input aria-label={t('addresses.value')} className={inputClass} placeholder={placeholders[a.type]} value={a.value} onChange={(e) => set({ value: e.target.value })} />
              {err('value')}
            </label>
          )}
          <label className="block">
            {t('addresses.comment')}
            <input aria-label={t('addresses.comment')} className={inputClass} value={a.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
        </>
      )}
    />
  )
}
