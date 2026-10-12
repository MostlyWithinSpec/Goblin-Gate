// Service groups (config "service_groups"): named sets of service objects.
import { Checklist, ConfigList } from '../../components/ConfigList'
import type { Group } from '../../api/objects'
import { validateGroup } from './networkLogic'

export default function ServiceGroups() {
  return (
    <ConfigList<Group>
      title="Service Groups"
      path="service_groups"
      newLabel="New service group"
      select={(c) => c.service_groups}
      empty={() => ({ name: '', members: [] })}
      validate={(g, cfg) => validateGroup(g, (cfg.services ?? []).map((x) => x.name), 'service')}
      columns={[{ label: 'Members', render: (g) => (g.members ?? []).join(', ') }]}
      form={(g, set, { cfg, err }) => (
        <>
          <Checklist label="Members" options={(cfg.services ?? []).map((s) => s.name)} value={g.members ?? []} onChange={(members) => set({ members })} />
          {err('members')}
        </>
      )}
    />
  )
}
