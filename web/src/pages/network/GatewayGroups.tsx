// Gateway groups (config "gateway_groups"): members with a tier (1 = first
// choice) and a weight (share within the tier). Used by policies as a single
// gateway choice that fails over.
import { ConfigList, inputClass } from '../../components/ConfigList'
import type { GatewayGroup, GatewayGroupMember } from '../../api/objects'
import { validateGatewayGroup } from './networkLogic'

export default function GatewayGroups() {
  return (
    <ConfigList<GatewayGroup>
      title="Gateway Groups"
      path="gateway_groups"
      newLabel="New group"
      select={(c) => c.gateway_groups}
      empty={(cfg) => ({ name: '', members: cfg.gateways?.length ? [{ gateway: cfg.gateways[0].name, tier: 1, weight: 1 }] : [] })}
      clean={(gg) => ({
        name: gg.name.trim(),
        members: gg.members.map((m) => ({ gateway: m.gateway, tier: m.tier ?? 1, weight: m.weight ?? 1 })),
      })}
      validate={(gg, cfg) => validateGatewayGroup(gg, cfg)}
      columns={[
        {
          label: 'Members',
          render: (gg) =>
            gg.members.map((m) => `${m.gateway} (tier ${m.tier ?? 1}, weight ${m.weight ?? 1})`).join(', ') || '—',
        },
      ]}
      form={(gg, set, { cfg, err }) => {
        const gateways = (cfg.gateways ?? []).map((g) => g.name)
        const members = gg.members
        const update = (i: number, patch: Partial<GatewayGroupMember>) =>
          set({ members: members.map((m, j) => (i === j ? { ...m, ...patch } : m)) })
        const remove = (i: number) => set({ members: members.filter((_, j) => j !== i) })
        const add = () => set({ members: [...members, { gateway: gateways[0] ?? '', tier: 1, weight: 1 }] })
        return (
          <div>
            {err('members')}
            {members.map((m, i) => (
              <div key={i} className="mb-2 grid grid-cols-[1fr_4rem_4rem_2rem] items-start gap-2">
                <label className="block">
                  Gateway
                  <select aria-label={`Gateway ${i + 1}`} className={inputClass} value={m.gateway} onChange={(e) => update(i, { gateway: e.target.value })}>
                    <option value="">Pick a gateway</option>
                    {gateways.map((n) => <option key={n} value={n}>{n}</option>)}
                  </select>
                  {err(`members.${i}.gateway`)}
                  {err(`members.${i}`)}
                </label>
                <label className="block">
                  Tier
                  <input aria-label={`Tier ${i + 1}`} type="number" min={1} max={5} className={inputClass} value={m.tier ?? 1} onChange={(e) => update(i, { tier: Number(e.target.value) })} />
                </label>
                <label className="block">
                  Weight
                  <input aria-label={`Weight ${i + 1}`} type="number" min={1} max={100} className={inputClass} value={m.weight ?? 1} onChange={(e) => update(i, { weight: Number(e.target.value) })} />
                </label>
                <button type="button" aria-label={`Remove member ${i + 1}`} className="mt-6 text-red-600" onClick={() => remove(i)}>✕</button>
              </div>
            ))}
            <button type="button" className="rounded border border-gray-300 px-2 py-1 text-sm text-gray-700 dark:border-gray-600 dark:text-gray-300" onClick={add}>
              Add member
            </button>
          </div>
        )
      }}
    />
  )
}
