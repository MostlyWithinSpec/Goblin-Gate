package render

import (
	"net/netip"
	"runtime"
	"sort"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// IPS queue layout: each IPS profile in use gets its own Suricata instance
// reading IPSQueuesPerProfile NFQUEUE queues (fanout spreads flows over
// them, one Suricata thread each). Profile i (by name order) uses queues
// IPSQueueBase+IPSQueueStride*i ... +IPSQueuesPerProfile-1.
const (
	IPSQueueBase   = 16
	IPSQueueStride = 8
	// MaxIPSProfiles is how many profiles fit in the conntrack mark's
	// profile byte (bits 8-15).
	MaxIPSProfiles = 254
)

// IPSQueuesPerProfile is one queue (and Suricata thread) per CPU core, at
// most 4 and never more than IPSQueueStride: more threads than cores only
// slow Suricata down (it warns about it). Tests pin it for stable output.
var IPSQueuesPerProfile = min(max(runtime.NumCPU(), 1), 4)

// IPSUse is an IPS profile that at least one enabled accept policy uses.
type IPSUse struct {
	Profile     config.IPSProfile
	Index       int    // 0-based, by profile name
	QueueFirst  int    // first NFQUEUE number
	QueueLast   int    // last NFQUEUE number
	ProfileMark uint32 // value of ct mark bits 8-15 (Index+1)<<8
}

// IPSProfilesInUse lists the profiles referenced by enabled accept policies,
// sorted by name, with their queues and marks.
func IPSProfilesInUse(cfg *config.Config) []IPSUse {
	used := map[string]bool{}
	for _, p := range cfg.Policies {
		if p.Enabled && p.Action == "accept" && p.IPSProfile != "" {
			used[p.IPSProfile] = true
		}
	}
	var profiles []config.IPSProfile
	for _, p := range cfg.Profiles.IPS {
		if used[p.Name] {
			profiles = append(profiles, p)
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	if len(profiles) > MaxIPSProfiles {
		profiles = profiles[:MaxIPSProfiles]
	}
	out := make([]IPSUse, len(profiles))
	for i, p := range profiles {
		first := IPSQueueBase + IPSQueueStride*i
		out[i] = IPSUse{Profile: p, Index: i, QueueFirst: first, QueueLast: first + IPSQueuesPerProfile - 1, ProfileMark: uint32(i+1) << 8}
	}
	return out
}

// InternalPrefixes are the subnets of enabled non-WAN interfaces and VLANs
// with a static address (Suricata's HOME_NET).
func InternalPrefixes(cfg *config.Config) []netip.Prefix {
	var out []netip.Prefix
	wan := map[string]bool{}
	for _, i := range cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if i.Enabled && i.Role != "wan" && i.Address != nil {
			out = append(out, i.Address.Masked())
		}
	}
	for _, v := range cfg.VLANs {
		if v.Enabled && !wan[v.Parent] && v.Address != nil {
			out = append(out, v.Address.Masked())
		}
	}
	return out
}
