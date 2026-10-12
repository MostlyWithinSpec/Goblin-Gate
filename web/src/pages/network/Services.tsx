// Service objects (config "services"): protocol and ports.
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import type { Service } from '../../api/objects'
import { validateService } from './networkLogic'

export default function Services() {
  return (
    <ConfigList<Service>
      title="Services"
      path="services"
      newLabel="New service"
      select={(c) => c.services}
      empty={() => ({ name: '', protocol: 'tcp', ports: '' })}
      clean={(s) => omitEmpty({ ...s, ports: s.protocol === 'tcp' || s.protocol === 'udp' ? s.ports?.replace(/\s+/g, '') : '', comment: s.comment?.trim() })}
      validate={(s) => validateService(s)}
      columns={[
        { label: 'Protocol', render: (s) => s.protocol.toUpperCase() },
        { label: 'Ports', render: (s) => s.ports },
        { label: 'Comment', render: (s) => s.comment },
      ]}
      form={(s, set, { err }) => (
        <>
          <label className="block">
            Protocol
            <select aria-label="Protocol" className={inputClass} value={s.protocol} onChange={(e) => set({ protocol: e.target.value as Service['protocol'] })}>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP</option>
              <option value="any">Any</option>
            </select>
          </label>
          {(s.protocol === 'tcp' || s.protocol === 'udp') && (
            <label className="block">
              Ports
              <input aria-label="Ports" className={inputClass} placeholder="80,443,8000-8100" value={s.ports ?? ''} onChange={(e) => set({ ports: e.target.value })} />
              {err('ports')}
            </label>
          )}
          <label className="block">
            Comment
            <input aria-label="Comment" className={inputClass} value={s.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
        </>
      )}
    />
  )
}
