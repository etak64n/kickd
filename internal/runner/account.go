package runner

import "sort"

// env lists the variables that name the user to the programs of a command,
// in the order of their names. TMPDIR leaves the temporary directory of the
// agent, which on macOS belongs to root and is closed to other users.
func (a *account) env() []string {
	vars := map[string]string{
		"HOME":    a.home,
		"USER":    a.name,
		"LOGNAME": a.name,
		"TMPDIR":  accountTempDir,
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + vars[k]
	}
	return out
}
