// Address groups (config "address_groups"): named sets of address objects.
import { Checklist, ConfigList } from '../../components/ConfigList'
import type { Group } from '../../api/objects'
import { validateGroup } from './networkLogic'
import { t } from '../../i18n'

export default function AddressGroups() {
  return (
    <ConfigList<Group>
      title={t('addressGroups.title')}
      path="address_groups"
      newLabel={t('addressGroups.newGroup')}
      select={(c) => c.address_groups}
      empty={() => ({ name: '', members: [] })}
      validate={(g, cfg) => validateGroup(g, (cfg.addresses ?? []).map((x) => x.name), 'address')}
      columns={[{ label: t('addressGroups.col.members'), render: (g) => (g.members ?? []).join(', ') }]}
      form={(g, set, { cfg, err }) => (
        <>
          <Checklist label={t('addressGroups.members')} options={(cfg.addresses ?? []).map((a) => a.name)} value={g.members ?? []} onChange={(members) => set({ members })} />
          {err('members')}
        </>
      )}
    />
  )
}
