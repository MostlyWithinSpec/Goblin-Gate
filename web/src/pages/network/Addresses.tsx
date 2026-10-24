// Address objects (config "addresses"): subnet, host, range, FQDN or country,
// named once and used by policies and address groups. Country values are a
// comma-joined list of ISO 3166-1 alpha-2 codes picked in the form.
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import { CountryPicker, GeoIPStatusCard, flagEmoji } from '../../components/GeoIP'
import type { Address } from '../../api/objects'
import { validateAddress } from './networkLogic'

const placeholders: Record<Exclude<Address['type'], 'country'>, string> = {
  subnet: '192.168.1.0/24 or 2001:db8::/32',
  host: '192.168.1.10 or 2001:db8::1',
  range: '192.168.1.100-192.168.1.150 or 2001:db8::1-2001:db8::ff',
  fqdn: 'example.com',
}

export default function Addresses() {
  return (
    <ConfigList<Address>
      title="Addresses"
      path="addresses"
      newLabel="New address"
      select={(c) => c.addresses}
      empty={() => ({ name: '', type: 'subnet', value: '' })}
      clean={(a) => ({ ...omitEmpty({ name: a.name, type: a.type, comment: a.comment?.trim() || undefined }), value: (a.value ?? '').trim() })}
      validate={(a) => validateAddress(a)}
      // Network → Devices "Create address object" links here with ?new_host=<ip>
      fromURL={(params) => {
        const ip = params.get('new_host')?.trim()
        return ip ? { name: `host_${ip.replace(/[^A-Za-z0-9_.-]/g, '_')}`, type: ip.includes('/') ? 'subnet' : 'host', value: ip } : null
      }}
      intro={<GeoIPStatusCard />}
      columns={[
        { label: 'Type', render: (a) => a.type },
        {
          label: 'Value',
          render: (a) =>
            a.type === 'country' ? (
              <span className="flex flex-wrap gap-1">
                {(a.value ?? '').split(',').filter(Boolean).map((code) => (
                  <span key={code} className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-xs dark:bg-gray-700">
                    {flagEmoji(code)} {code}
                  </span>
                ))}
              </span>
            ) : (
              <code>{a.value}</code>
            ),
        },
        { label: 'Comment', render: (a) => a.comment },
      ]}
      form={(a, set, { err }) => (
        <>
          <label className="block">
            Type
            <select aria-label="Type" className={inputClass} value={a.type} onChange={(e) => set({ type: e.target.value as Address['type'] })}>
              <option value="subnet">Subnet</option>
              <option value="host">Host</option>
              <option value="range">IP range</option>
              <option value="fqdn">FQDN</option>
              <option value="country">Country</option>
            </select>
          </label>
          {a.type === 'country' ? (
            <div className="block">
              Countries
              <CountryPicker value={a.value} onChange={(v) => set({ value: v })} />
              {err('value')}
            </div>
          ) : (
            <label className="block">
              Value
              <input aria-label="Value" className={inputClass} placeholder={placeholders[a.type]} value={a.value} onChange={(e) => set({ value: e.target.value })} />
              {err('value')}
            </label>
          )}
          <label className="block">
            Comment
            <input aria-label="Comment" className={inputClass} value={a.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
        </>
      )}
    />
  )
}
