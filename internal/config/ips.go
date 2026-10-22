package config

import "regexp"

// IPSSettings are box-wide IPS settings (the profiles are in Profiles.IPS).
type IPSSettings struct {
	// ETProCode is an ET Pro subscription code (task 114, a secret): with it
	// the rule update fetches ET Pro instead of ET Open.
	ETProCode string `json:"etpro_code,omitempty"`
}

// etProCodeRe: Proofpoint's codes are hex; anything else could not be
// written safely into suricata-update's source file.
var etProCodeRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

func (v *validator) checkIPSSettings() {
	if c := v.cfg.IPS.ETProCode; c != "" && !etProCodeRe.MatchString(c) {
		v.add("ips.etpro_code", "want the ET Pro code from your Proofpoint account (8-64 letters and digits)")
	}
}

// ValidETProCode reports whether s can be an ET Pro code.
func ValidETProCode(s string) bool { return etProCodeRe.MatchString(s) }
