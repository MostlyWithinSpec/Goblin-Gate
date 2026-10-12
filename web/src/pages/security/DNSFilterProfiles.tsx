// DNS filter profiles (config "profiles.dnsfilter", task 029): block domain
// feeds and lists in Unbound for the interfaces whose policies use them.
import { Checklist, ConfigList, ListInput, inputClass, omitEmpty } from '../../components/ConfigList'
import { profileInterfaces, type DNSFilterProfile } from '../../api/objects'
import { validateDomainLists } from './profileLogic'

export default function DNSFilterProfiles() {
  return (
    <div className="p-6">
      <ConfigList<DNSFilterProfile>
        title="DNS Filter Profiles"
        path="profiles.dnsfilter"
        newLabel="New DNS filter profile"
        intro="A profile applies to the source interfaces of the first policy that selects it. Allowed domains win over every block list; a listed name also covers its subdomains."
        select={(c) => c.profiles?.dnsfilter}
        empty={() => ({ name: '', block_feeds: [], block_domains: [], allow_domains: [], action: 'nxdomain' })}
        clean={(p) => omitEmpty({ ...p, comment: p.comment?.trim(), safe_search: p.safe_search || undefined, action: p.action === 'nxdomain' ? undefined : p.action })}
        validate={(p) => validateDomainLists(p)}
        columns={[
          { label: 'Feeds', render: (p) => (p.block_feeds ?? []).join(', ') },
          { label: 'Domains', render: (p) => `${(p.block_domains ?? []).length} blocked, ${(p.allow_domains ?? []).length} allowed` },
          { label: 'Safe search', render: (p) => (p.safe_search ? 'on' : '') },
          { label: 'Answer', render: (p) => (p.action === 'blockpage' ? 'block page' : 'NXDOMAIN') },
        ]}
        after={(cfg, items) => {
          const used = profileInterfaces(cfg, 'dnsfilter_profile', items.map((p) => p.name))
          return <UsedBy used={used} />
        }}
        form={(p, set, { cfg, err }) => (
          <>
            <label className="block">
              Comment
              <input aria-label="Comment" className={inputClass} value={p.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            </label>
            <Checklist
              label="Block domain feeds"
              options={(cfg.feeds ?? []).filter((f) => f.type === 'domain').map((f) => f.name)}
              value={p.block_feeds ?? []}
              onChange={(block_feeds) => set({ block_feeds })}
            />
            <ListInput label="Always block" placeholder={'ads.example.com\nexample.net'} value={p.block_domains} onChange={(block_domains) => set({ block_domains })} />
            {err('block_domains')}
            <ListInput label="Never block" placeholder="allowed.example.com" value={p.allow_domains} onChange={(allow_domains) => set({ allow_domains })} />
            {err('allow_domains')}
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={!!p.safe_search} onChange={(e) => set({ safe_search: e.target.checked })} /> Force safe search (Google, Bing, DuckDuckGo, YouTube)
            </label>
            <label className="block">
              Blocked names answer with
              <select aria-label="Answer" className={inputClass} value={p.action ?? 'nxdomain'} onChange={(e) => set({ action: e.target.value as DNSFilterProfile['action'] })}>
                <option value="nxdomain">NXDOMAIN (name does not exist)</option>
                <option value="blockpage">The block page</option>
              </select>
            </label>
          </>
        )}
      />
    </div>
  )
}

// UsedBy lists the interfaces each profile applies to.
export function UsedBy({ used }: { used: Record<string, string[]> }) {
  const rows = Object.entries(used)
  return (
    <p className="mt-3 text-sm text-gray-600 dark:text-gray-400" data-testid="used-by">
      {rows.length === 0
        ? 'No policy uses these profiles yet: pick one in a policy to apply it.'
        : rows.map(([prof, ifs]) => `${prof}: ${ifs.join(', ')}`).join(' · ')}
    </p>
  )
}
