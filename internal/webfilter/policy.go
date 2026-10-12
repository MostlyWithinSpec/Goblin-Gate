package webfilter

import (
	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Actions, weakest first.
const (
	ActionAllow   = "allow"
	ActionMonitor = "monitor"
	ActionWarn    = "warn"
	ActionBlock   = "block"
)

var strength = map[string]int{ActionAllow: 0, ActionMonitor: 1, ActionWarn: 2, ActionBlock: 3}

// Decision is what a profile does with a name.
type Decision struct {
	Action   string `json:"action"`
	Category string `json:"category,omitempty"` // the category that decided ("custom" for the profile's own lists)
	Match    string `json:"match,omitempty"`    // the listed name that matched (the name or a parent)
}

// Profile is a compiled web filter profile.
type Profile struct {
	Name       string
	categories map[string]string
	block      map[string]bool
	allow      map[string]bool
}

// Compile prepares a profile for lookups.
func Compile(p config.WebFilterProfile) *Profile {
	c := &Profile{Name: p.Name, categories: p.Categories, block: map[string]bool{}, allow: map[string]bool{}}
	for _, d := range p.BlockDomains {
		if n := normalize(d); n != "" {
			c.block[n] = true
		}
	}
	for _, d := range p.AllowDomains {
		if n := normalize(d); n != "" {
			c.allow[n] = true
		}
	}
	return c
}

// Decide applies the profile to a name: the allow list wins, then the block
// list, then the strongest action among the name's categories.
func (p *Profile) Decide(db *DB, name string) Decision {
	suffixes := Suffixes(normalize(name))
	for _, s := range suffixes {
		if p.allow[s] {
			return Decision{Action: ActionAllow, Category: "custom", Match: s}
		}
	}
	for _, s := range suffixes {
		if p.block[s] {
			return Decision{Action: ActionBlock, Category: "custom", Match: s}
		}
	}
	best := Decision{Action: ActionAllow}
	if len(p.categories) == 0 || db == nil {
		return best
	}
	// the most specific listed name decides, and is what a warn
	// click-through allows (with its subdomains)
	for _, s := range suffixes {
		for _, cat := range db.exact(s) {
			a := p.categories[cat]
			if a == "" {
				a = ActionAllow
			}
			if strength[a] > strength[best.Action] {
				best = Decision{Action: a, Category: cat, Match: s}
			}
		}
		if best.Action != ActionAllow {
			break
		}
	}
	return best
}

// AppProfile is a compiled application control profile: app id -> action.
type AppProfile struct {
	Name    string
	actions map[string]string
}

// CompileApps resolves group actions and per-app overrides.
func CompileApps(p config.AppCtrlProfile) *AppProfile {
	c := &AppProfile{Name: p.Name, actions: map[string]string{}}
	for _, a := range appctrl.Catalog() {
		if act, ok := p.Groups[a.Group]; ok {
			c.actions[a.ID] = act
		}
	}
	for id, act := range p.Apps {
		c.actions[id] = act
	}
	return c
}

// Decide returns block or monitor for a name of a controlled app (category
// "app:<id>"), else allow.
func (p *AppProfile) Decide(name string) Decision {
	id, match, ok := appctrl.Identify(name)
	if !ok {
		return Decision{Action: ActionAllow}
	}
	act := p.actions[id]
	if act != ActionBlock && act != ActionMonitor {
		return Decision{Action: ActionAllow}
	}
	return Decision{Action: act, Category: "app:" + id, Match: match}
}
