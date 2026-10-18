import { describe, expect, test } from 'vitest'
import { usedBy } from '../components/usage'
import type { Candidate } from '../api/objects'

describe('usedBy', () => {
  const cfg: Candidate = {
    addresses: [{ name: 'na', type: 'country', value: 'US,CA' }],
    address_groups: [{ name: 'trusted', members: ['na'] }],
    nat: { port_forwards: [{ name: 'plex', source: ['na'] }, { name: 'ssh', source: ['trusted'] }, { name: 'web' }] },
  }

  test('port forward sources count as uses', () => {
    expect(usedBy('addresses', 'na', cfg)).toEqual(['port forward "plex"', 'address group "trusted"'])
    expect(usedBy('address_groups', 'trusted', cfg)).toEqual(['port forward "ssh"'])
  })
})
