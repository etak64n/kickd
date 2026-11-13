package runner

import "sort"

// account is the user whose rights a command runs with when its event names
// a user.
type account struct {
	name   string
	uid    uint32
	gid    uint32
	groups []uint32
	home   string
}

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
