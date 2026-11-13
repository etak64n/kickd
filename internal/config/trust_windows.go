//go:build windows

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows, the access control list of a file or folder, a list of rules
// that each allow or deny rights to an account, decides who can change it,
// and its owner can always change that list. The agent for the whole
// machine runs the commands of its config as SYSTEM, so it reads the config
// only when SYSTEM and the administrators alone can change the config
// folder, the config file and each other file of events. Whoever can add a
// file to the config folder can add events.

// The accounts that may own and change the config for the whole machine,
// as security identifiers, which are the same in every language of
// Windows.
const (
	sidSystem           = "S-1-5-18"
	sidAdministrators   = "S-1-5-32-544"
	sidTrustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
)

// trusted reports whether sid, as a string, is an account that may own and
// change the config for the whole machine.
func trusted(sid string) bool {
	return sid == sidSystem || sid == sidAdministrators || sid == sidTrustedInstaller
}

// systemFolderSDDL is the security descriptor that kickd init gives the
// folder of the config for the whole machine: an access control list that
// does not inherit the rules of the folder above it, and gives SYSTEM and
// the Administrators group full control of the folder and of the files and
// folders in it, which inherit both rules.
const systemFolderSDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// MkdirConfig creates dir, the folder of a new config file, with the
// folders above it. For the config for the whole machine, dir gets the
// access control list of systemFolderSDDL. A folder that exists keeps the
// access control list that it has, which the check of Load judges.
func MkdirConfig(dir string, system bool) error {
	if !system {
		return os.MkdirAll(dir, 0o755)
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(systemFolderSDDL)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, sa); err != nil {
		return &fs.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	return nil
}

// Rights of a rule that change a file or folder: write data or add a file,
// append data or add a subfolder, write extended attributes, write
// attributes, delete what a folder holds, delete it, change its access
// control list, change its owner, and the generic rights that include
// them.
const (
	fileDeleteChild = 0x40 // FILE_DELETE_CHILD, which x/sys/windows lacks
	changeRights    = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild | windows.DELETE | windows.WRITE_DAC |
		windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
)

// accessAllowedCallbackACEType is the type of a rule that allows rights
// under a condition, which x/sys/windows lacks. The check counts it as a
// rule that allows them.
const accessAllowedCallbackACEType = 9

// checkRootOnly reports each problem that lets an account other than
// SYSTEM, the Administrators group and TrustedInstaller change the config
// for the whole machine: the config folder, the config file, or another
// file of events, with the command that fixes it. paths are the config
// file and the other files of events, in the config folder.
func checkRootOnly(paths []string) error {
	var errs []error
	dir := filepath.Dir(paths[0])
	for _, err := range adminsOnlyAt(dir) {
		errs = append(errs, fmt.Errorf("%s: kickd runs the commands of the YAML files in this folder as SYSTEM, so only SYSTEM and administrators may be able to change it: %w", dir, err))
	}
	for _, p := range paths {
		for _, err := range adminsOnlyAt(p) {
			errs = append(errs, fmt.Errorf("%s: kickd runs the commands of this file as SYSTEM, so only SYSTEM and administrators may be able to change it: %w", p, err))
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Problems: errs}
	}
	return nil
}

// aclEntry is a file or folder whose owner and access control list the
// check judges.
type aclEntry struct {
	path string
	dir  bool // a folder, whose rules the files in it inherit
	link bool // a link itself, rather than what it points to
}

// adminsOnlyAt judges the file or folder at path and, when path is a link,
// what the link points to as well: whoever can change the link can point
// it elsewhere.
func adminsOnlyAt(path string) []error {
	self, err := openSecurity(path, false)
	if err != nil {
		return []error{err}
	}
	if self.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return adminsOnly(aclEntry{path: path, dir: self.dir()}, self.sd)
	}
	target, err := openSecurity(path, true)
	if err != nil {
		return []error{err}
	}
	if target.id == self.id {
		return adminsOnly(aclEntry{path: path, dir: self.dir()}, self.sd)
	}
	return append(adminsOnly(aclEntry{path: path, dir: self.dir(), link: true}, self.sd),
		adminsOnly(aclEntry{path: target.path, dir: target.dir()}, target.sd)...)
}

// security is what the check reads of a file or folder.
type security struct {
	path  string
	sd    *windows.SECURITY_DESCRIPTOR
	attrs uint32
	id    [3]uint32 // the volume and the index of the file, which name it
}

func (s security) dir() bool { return s.attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 }

// openSecurity reads the owner and the access control list of the file or
// folder at path. Through a link, it reads those of what the link points
// to when follow is true, with the path that Windows gives it, and those
// of the link itself otherwise.
func openSecurity(path string, follow bool) (security, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return security{}, err
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS) // which opens folders too
	if !follow {
		flags |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return security{}, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	defer windows.CloseHandle(h)
	s := security{path: path}
	if s.sd, err = windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION); err != nil {
		return security{}, &fs.PathError{Op: "read the access control list of", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return security{}, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	s.attrs, s.id = info.FileAttributes, [3]uint32{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}
	if follow {
		s.path = finalPath(h, path)
	}
	return s, nil
}

// finalPath returns the path that Windows gives the open file h, without
// the prefix \\?\, or path when Windows gives none.
func finalPath(h windows.Handle, path string) string {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || int(n) >= len(buf) {
		return path
	}
	p := windows.UTF16ToString(buf[:n])
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}

// adminsOnly returns the problems that let an account other than SYSTEM,
// the Administrators group and TrustedInstaller change e, whose security
// descriptor is sd: an owner that is not one of them, and the rules of
// its access control list that allow another account a right that changes
// e. A rule that applies only to what a folder holds does not count, and
// neither does a rule that denies rights. Each problem names the icacls
// command that fixes it.
func adminsOnly(e aclEntry, sd *windows.SECURITY_DESCRIPTOR) []error {
	var errs []error
	switch owner, _, err := sd.Owner(); {
	case err != nil:
		return []error{err}
	case owner == nil:
		errs = append(errs, fmt.Errorf("%s has no owner; make the Administrators group its owner with %s", e.name(), e.icacls("/setowner", "*"+sidAdministrators)))
	case !trusted(owner.String()):
		errs = append(errs, fmt.Errorf("%s belongs to %s; make the Administrators group its owner with %s", e.name(), account(owner), e.icacls("/setowner", "*"+sidAdministrators)))
	}
	dacl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || (err == nil && dacl == nil) {
		return append(errs, fmt.Errorf("%s has no access control list, so anyone can change it; give only SYSTEM and the Administrators group access to it with %s", e.name(), e.icacls(e.grant()...)))
	}
	if err != nil {
		return append(errs, err)
	}
	var others, remove []string
	named, removed := map[string]bool{}, map[string]bool{}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return append(errs, err)
		}
		h := ace.Header
		if (h.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && h.AceType != accessAllowedCallbackACEType) ||
			h.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask&changeRights == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		s := sid.String()
		if trusted(s) {
			continue
		}
		if !named[s] {
			named[s] = true
			others = append(others, account(sid))
		}
		// /inheritance:r removes the rules that e inherits, and /remove:g
		// those that e has itself.
		if h.AceFlags&windows.INHERITED_ACE == 0 && !removed[s] {
			removed[s] = true
			remove = append(remove, "*"+s)
		}
	}
	if len(others) > 0 {
		args := e.grant()
		if len(remove) > 0 {
			args = append(append(args, "/remove:g"), remove...)
		}
		errs = append(errs, fmt.Errorf("%s can be changed by %s; give only SYSTEM and the Administrators group access to it with %s", e.name(), strings.Join(others, " and "), e.icacls(args...)))
	}
	return errs
}

// name names e in a message.
func (e aclEntry) name() string {
	if e.link {
		return "the link " + e.path
	}
	return e.path
}

// grant returns the arguments of icacls that make the access control list
// of e give full control to SYSTEM and the Administrators group: e stops
// inheriting the rules of the folder above it, and gets a rule for each of
// the two, which the files and folders in a folder inherit.
func (e aclEntry) grant() []string {
	rights := "F"
	if e.dir {
		rights = "(OI)(CI)F"
	}
	return []string{"/inheritance:r", "/grant:r", `"*` + sidSystem + ":" + rights + `"`, `"*` + sidAdministrators + ":" + rights + `"`}
}

// icacls returns the icacls command line that runs args on e. It names
// accounts by their security identifiers, which work in every language of
// Windows, and quotes the path and the arguments with parentheses, which
// PowerShell would read otherwise.
func (e aclEntry) icacls(args ...string) string {
	cmd := []string{"icacls", `"` + e.path + `"`}
	if e.link {
		cmd = append(cmd, "/L")
	}
	return strings.Join(append(cmd, args...), " ")
}

// account names the account sid in a message: its name, when Windows knows
// it, and its security identifier.
func account(sid *windows.SID) string {
	name, domain, _, err := sid.LookupAccount("")
	switch {
	case err != nil || name == "":
		return sid.String()
	case domain != "":
		name = domain + `\` + name
	}
	return name + " (" + sid.String() + ")"
}
