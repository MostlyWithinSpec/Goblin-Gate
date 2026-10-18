package logexport

import (
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// redact strips any secrets from err so it is safe to store in Status and
// show in the API. The password, token and a URL with embedded credentials
// are all replaced.
func redact(c config.LogExport, err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	for _, secret := range []string{c.Password, c.Token} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "****")
		}
	}
	if u := stripUserinfo(c.URL); u != c.URL && u != "" {
		s = strings.ReplaceAll(s, c.URL, u)
		// Also strip the userinfo prefix in case only a partial URL appears.
		if scheme, userinfo, ok := urlUserinfoPrefix(c.URL); ok {
			s = strings.ReplaceAll(s, scheme+userinfo, scheme)
		}
	}
	return errRedacted(s)
}

type errRedacted string

func (e errRedacted) Error() string { return string(e) }

// urlUserinfoPrefix returns the scheme (including "://") and the userinfo
// portion (e.g. "user:pass@") of u. ok is false when u has no userinfo.
func urlUserinfoPrefix(u string) (scheme, userinfo string, ok bool) {
	for i := 0; i < len(u)-3; i++ {
		if u[i] == ':' && u[i+1] == '/' && u[i+2] == '/' {
			scheme = u[:i+3]
			rest := u[i+3:]
			slash := strings.IndexByte(rest, '/')
			if slash < 0 {
				slash = len(rest)
			}
			hostPart := rest[:slash]
			if at := strings.LastIndexByte(hostPart, '@'); at > 0 {
				userinfo = hostPart[:at+1]
				ok = true
			}
			return
		}
	}
	return "", "", false
}

// stripUserinfo removes user:pass@ from a URL, leaving scheme://host/path.
func stripUserinfo(u string) string {
	scheme := ""
	rest := u
	for i := 0; i < len(rest)-3; i++ {
		if rest[i] == ':' && rest[i+1] == '/' && rest[i+2] == '/' {
			scheme = rest[:i+3]
			rest = rest[i+3:]
			break
		}
	}
	// Strip userinfo: find the first '/' and look for '@' before it.
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		slash = len(rest)
	}
	hostPart := rest[:slash]
	pathPart := rest[slash:]
	if at := strings.LastIndexByte(hostPart, '@'); at > 0 {
		hostPart = hostPart[at+1:]
	}
	return scheme + hostPart + pathPart
}
