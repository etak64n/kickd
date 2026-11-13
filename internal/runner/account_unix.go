//go:build !windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// accountIDs are the user ID, the group ID and the other groups of an
// account, which a command starts with.
type accountIDs struct {
	uid    uint32
	gid    uint32
	groups []uint32
}

// accountTempDir is the temporary directory of a command that runs as the
// user of its event: a directory that every user can write to, where each
// file stays with its owner.
const accountTempDir = "/tmp"

// lookupAccount finds the user name, with its groups, in the accounts of the
// machine. Without cgo, which the release builds do without, macOS asks the
// directory service of the system, and Linux reads /etc/passwd and
// /etc/group.
func lookupAccount(name string) (*account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("user %s: uid %q: %w", name, u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("user %s: gid %q: %w", name, u.Gid, err)
	}
	ids, err := u.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("groups of user %s: %w", name, err)
	}
	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		g, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("groups of user %s: gid %q: %w", name, id, err)
		}
		groups = append(groups, uint32(g))
	}
	return &account{name: u.Username, home: u.HomeDir, accountIDs: accountIDs{uid: uint32(uid), gid: uint32(gid), groups: groups}}, nil
}

// runAs makes cmd start with the user, the group and the other groups of a.
// It goes after setSysProcAttr, which sets SysProcAttr anew.
func runAs(cmd *exec.Cmd, a *account) {
	if a == nil {
		return
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: a.uid, Gid: a.gid, Groups: a.groups}
}

// giveTo makes the file at path belong to a, so that a command that runs as
// a can read it.
func giveTo(path string, a *account) error {
	if a == nil {
		return nil
	}
	return os.Chown(path, int(a.uid), int(a.gid))
}
