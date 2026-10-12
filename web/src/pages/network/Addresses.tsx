// Address objects (config "addresses"): subnet, host, range or FQDN, named
// once and used by policies and address groups.
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import type { Address } from '../../api/objects'
import { validateAddress } from './networkLogic'

const placeholders: Record<Address['type'], string> = {
  subnet: '192.168.1.0/24',
  host: '192.168.1.10',
  range: '192.168.1.100-192.168.1.150',
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
      clean={(a) => omitEmpty({ ...a, value: a.value.trim(), comment: a.comment?.trim() })}
      validate={(a) => validateAddress(a)}
      columns={[
        { label: 'Type', render: (a) => a.type },
        { label: 'Value', render: (a) => <code>{a.value}</code> },
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
            </select>
          </label>
          <label className="block">
            Value
            <input aria-label="Value" className={inputClass} placeholder={placeholders[a.type]} value={a.value} onChange={(e) => set({ value: e.target.value })} />
            {err('value')}
          </label>
          <label className="block">
            Comment
            <input aria-label="Comment" className={inputClass} value={a.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
        </>
      )}
    />
  )
}
