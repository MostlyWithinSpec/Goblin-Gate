// Address groups (config "address_groups"): named sets of address objects.
import { Checklist, ConfigList } from '../../components/ConfigList'
import type { Group } from '../../api/objects'
import { validateGroup } from './networkLogic'

export default function AddressGroups() {
  return (
    <ConfigList<Group>
      title="Address Groups"
      path="address_groups"
      newLabel="New address group"
      select={(c) => c.address_groups}
      empty={() => ({ name: '', members: [] })}
      validate={(g, cfg) => validateGroup(g, (cfg.addresses ?? []).map((x) => x.name), 'address')}
      columns={[{ label: 'Members', render: (g) => (g.members ?? []).join(', ') }]}
      form={(g, set, { cfg, err }) => (
        <>
          <Checklist label="Members" options={(cfg.addresses ?? []).map((a) => a.name)} value={g.members ?? []} onChange={(members) => set({ members })} />
          {err('members')}
        </>
      )}
    />
  )
}
