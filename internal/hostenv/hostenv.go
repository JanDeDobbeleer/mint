// Package hostenv builds the filtered environment Mint gives to processes it starts on the host.
package hostenv

import (
	"slices"
	"strings"
)

// Filter returns environ reduced to an allowlist of variables that processes need to run,
// followed by overrides in name order. Inherited tokens and any unlisted variable are dropped,
// and an override replaces an inherited variable with the same (case-insensitive) name.
func Filter(environ []string, overrides map[string]string) []string {
	allowed := map[string]bool{}
	for name := range strings.FieldsSeq(`PATH PATHEXT SYSTEMROOT WINDIR COMSPEC SYSTEMDRIVE COPILOT_HOME
		HOME USERPROFILE HOMEDRIVE HOMEPATH APPDATA LOCALAPPDATA PROGRAMDATA ALLUSERSPROFILE
		PROGRAMFILES PROGRAMFILES(X86) PROGRAMW6432 COMMONPROGRAMFILES COMMONPROGRAMFILES(X86)
		TEMP TMP TMPDIR USER USERNAME USERDOMAIN LOGNAME SHELL PSModulePath
		LANG LANGUAGE TERM COLORTERM TERM_PROGRAM TERM_PROGRAM_VERSION NO_COLOR CLICOLOR
		WT_SESSION WT_PROFILE_ID NUMBER_OF_PROCESSORS PROCESSOR_ARCHITECTURE
		SSL_CERT_FILE SSL_CERT_DIR NODE_EXTRA_CA_CERTS`) {
		allowed[strings.ToUpper(name)] = true
	}
	env := make([]string, 0, len(environ)+len(overrides))
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if !found || (!allowed[upper] && !strings.HasPrefix(upper, "LC_")) {
			continue
		}
		if _, overridden := overrides[upper]; overridden {
			continue
		}
		env = append(env, entry)
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		env = append(env, name+"="+overrides[name])
	}
	return env
}
