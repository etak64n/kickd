//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// sd builds a security descriptor from its SDDL string.
func sd(t *testing.T, sddl string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	d, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("%s: %v", sddl, err)
	}
	return d
}

// The config folder and the config file for the whole machine.
var (
	configFolder = aclEntry{path: `C:\ProgramData\kickd`, dir: true}
	configFile   = aclEntry{path: `C:\ProgramData\kickd\config.yaml`}
)

func TestAdminsOnlyAcceptsWhatOnlySystemAndAdministratorsCanChange(t *testing.T) {
	for _, c := range []struct{ name, sddl string }{
		{"the folder that kickd init creates", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"},
		{"a file that inherits the rules of that folder", "O:BAD:(A;ID;FA;;;SY)(A;ID;FA;;;BA)"},
		{"an owner that is SYSTEM", "O:SYD:(A;;FA;;;SY)"},
		{"an owner that is TrustedInstaller", "O:" + sidTrustedInstaller + "D:(A;;FA;;;" + sidTrustedInstaller + ")(A;;FA;;;BA)"},
		{"a rule that lets users read and run", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"},
		{"a rule only for what the folder holds", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICIIO;GA;;;CO)(A;OICIIO;FA;;;BU)"},
		{"a rule that denies users", "O:BAD:P(D;;FA;;;BU)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"},
		{"an empty access control list", "O:BAD:P"},
		{"a rule for the built-in Administrator, a member of the Administrators group", "O:BAD:(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)(A;OICIID;FA;;;LA)"},
	} {
		if errs := adminsOnly(configFolder, sd(t, c.sddl)); len(errs) > 0 {
			t.Errorf("%s: %v", c.name, errors.Join(errs...))
		}
	}
}

func TestAdminsOnlyRefusesWhatOtherAccountsCanChange(t *testing.T) {
	// The messages name an account by its name, which depends on the
	// language of Windows, and by its security identifier, which does not.
	const admins = "(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	const users = "(S-1-5-32-545); give only SYSTEM"
	for _, c := range []struct{ name, sddl, want string }{
		{"the folder that os.MkdirAll creates in ProgramData",
			"O:BAD:AI(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)(A;OICIIOID;GA;;;CO)(A;OICIID;0x1200a9;;;BU)(A;CIID;DCLCRPCR;;;BU)", users},
		{"an owner that is not trusted", "O:BUD:P" + admins, "(S-1-5-32-545); make the Administrators group its owner"},
		{"write data", "O:BAD:P" + admins + "(A;;0x2;;;BU)", users},
		{"append data", "O:BAD:P" + admins + "(A;;0x4;;;BU)", users},
		{"write extended attributes", "O:BAD:P" + admins + "(A;;0x10;;;BU)", users},
		{"delete what the folder holds", "O:BAD:P" + admins + "(A;;0x40;;;BU)", users},
		{"write attributes", "O:BAD:P" + admins + "(A;;0x100;;;BU)", users},
		{"delete", "O:BAD:P" + admins + "(A;;SD;;;BU)", users},
		{"change the access control list", "O:BAD:P" + admins + "(A;;WD;;;BU)", users},
		{"change the owner", "O:BAD:P" + admins + "(A;;WO;;;BU)", users},
		{"generic write", "O:BAD:P" + admins + "(A;;GW;;;BU)", users},
		{"generic all", "O:BAD:P" + admins + "(A;;GA;;;BU)", users},
		{"a rule with a condition", "O:BAD:P" + admins + "(XA;;FA;;;BU;(Member_of {SID(BA)}))", users},
		{"everyone", "O:BAD:P" + admins + "(A;;FA;;;WD)", "(S-1-1-0); give only SYSTEM"},
		{"authenticated users", "O:BAD:P" + admins + "(A;OICI;0x1301bf;;;AU)", "(S-1-5-11); give only SYSTEM"},
		{"no access control list", "O:BA", "has no access control list"},
		{"an access control list that lets anyone in", "O:BAD:NO_ACCESS_CONTROL", "has no access control list"},
	} {
		err := errors.Join(adminsOnly(configFolder, sd(t, c.sddl))...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// On the desktop versions of Windows, the files that an administrator
// creates belong to the account of the administrator, which is a member of
// the Administrators group.
func TestAdminsOnlyAcceptsAnOwnerInTheAdministratorsGroup(t *testing.T) {
	needsAdministrator(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !administrator(user.User.Sid) {
		t.Skip("the account of the test belongs to the Administrators group only through another group")
	}
	me := user.User.Sid.String()
	if errs := adminsOnly(configFile, sd(t, "O:"+me+"D:(A;;FA;;;"+me+")(A;ID;FA;;;SY)(A;ID;FA;;;BA)")); len(errs) > 0 {
		t.Errorf("a file of the administrator who runs the test: %v", errors.Join(errs...))
	}
}

// The error names the entry, and ends in the icacls command that fixes it:
// the command removes the rules that the entry inherits and those of other
// accounts that it has itself, and gives SYSTEM and the Administrators
// group full control.
func TestAdminsOnlyNamesTheFix(t *testing.T) {
	for _, c := range []struct {
		name string
		e    aclEntry
		sddl string
		want string
	}{
		{"a folder with the rules of ProgramData", configFolder,
			"O:BAD:AI(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)(A;OICIIOID;GA;;;CO)(A;OICIID;0x1200a9;;;BU)(A;CIID;DCLCRPCR;;;BU)",
			`icacls "C:\ProgramData\kickd" /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F"`},
		{"a file with a rule of its own", configFile,
			"O:BAD:AI(A;;0x100116;;;BU)(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
			`icacls "C:\ProgramData\kickd\config.yaml" /inheritance:r /grant:r "*S-1-5-18:F" "*S-1-5-32-544:F" /remove:g *S-1-5-32-545`},
		{"a file of another owner", configFile,
			"O:BUD:(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
			`icacls "C:\ProgramData\kickd\config.yaml" /setowner *S-1-5-32-544`},
		{"a link", aclEntry{path: `C:\ProgramData\kickd\app.yaml`, link: true},
			"O:BAD:(A;;FA;;;BU)(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
			`icacls "C:\ProgramData\kickd\app.yaml" /L /inheritance:r /grant:r "*S-1-5-18:F" "*S-1-5-32-544:F" /remove:g *S-1-5-32-545`},
	} {
		errs := adminsOnly(c.e, sd(t, c.sddl))
		if len(errs) != 1 {
			t.Errorf("%s: %d problems, want 1: %v", c.name, len(errs), errors.Join(errs...))
			continue
		}
		if got := errs[0].Error(); !strings.HasPrefix(got, c.e.name()+" ") || !strings.HasSuffix(got, " with "+c.want) {
			t.Errorf("%s:\n%s\nwant a message about %s that ends with\n%s", c.name, got, c.e.name(), c.want)
		}
	}
}

// needsAdministrator skips a test that needs kickd to run for the whole
// machine: in a PowerShell opened as administrator, as on the runners of
// the CI.
func needsAdministrator(t *testing.T) {
	t.Helper()
	if !System() {
		t.Skip("needs an administrator")
	}
}

// machineConfig makes a new folder the ProgramData folder of the test, and
// returns the folder of the config for the whole machine in it.
func machineConfig(t *testing.T) string {
	t.Helper()
	noLogEnv(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramData", dir)
	return filepath.Join(dir, "kickd")
}

func TestMkdirConfigOfTheWholeMachineLetsOnlySystemAndAdministratorsIn(t *testing.T) {
	needsAdministrator(t)
	dir := machineConfig(t)
	if err := MkdirConfig(dir, true); err != nil {
		t.Fatal(err)
	}
	d, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.String(); got != systemFolderSDDL {
		t.Errorf("the access control list of %s is %s, want %s", dir, got, systemFolderSDDL)
	}
}

func TestLoadReadsTheConfigForTheWholeMachineInTheFolderThatInitCreates(t *testing.T) {
	needsAdministrator(t)
	dir := machineConfig(t)
	if err := MkdirConfig(dir, true); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"config.yaml": event("backup"), "deploy.yaml": event("deploy")})
	if _, err := Load(Path()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRefusesTheConfigForTheWholeMachineInAFolderThatOtherAccountsCanChange(t *testing.T) {
	needsAdministrator(t)
	// The folder inherits the rules of the temporary folder of the user,
	// which give the user full control.
	dir := writeFiles(t, machineConfig(t), map[string]string{"config.yaml": event("backup")})
	_, err := Load(Path())
	for _, want := range []string{dir + ": kickd runs the commands of the YAML files in this folder as SYSTEM", "can be changed by", `icacls "` + dir + `" /inheritance:r`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func TestLoadRefusesAFileOfEventsThatLinksToAFileThatOtherAccountsCanChange(t *testing.T) {
	needsAdministrator(t)
	dir := machineConfig(t)
	if err := MkdirConfig(dir, true); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"config.yaml": event("backup")})
	target := filepath.Join(writeFiles(t, t.TempDir(), map[string]string{"linked-events.yaml": event("deploy")}), "linked-events.yaml")
	if err := os.Symlink(target, filepath.Join(dir, "deploy.yaml")); err != nil {
		t.Skip(err)
	}
	_, err := Load(Path())
	if err == nil || !strings.Contains(err.Error(), `linked-events.yaml can be changed by`) {
		t.Errorf("err = %v", err)
	}
}

func TestLoadRefusesALinkThatOtherAccountsCanChange(t *testing.T) {
	needsAdministrator(t)
	dir := machineConfig(t)
	other := filepath.Join(filepath.Dir(dir), "other")
	for _, d := range []string{dir, other} {
		if err := MkdirConfig(d, true); err != nil {
			t.Fatal(err)
		}
	}
	writeFiles(t, dir, map[string]string{"config.yaml": event("backup")})
	writeFiles(t, other, map[string]string{"deploy.yaml": event("deploy")})
	link := filepath.Join(dir, "deploy.yaml")
	if err := os.Symlink(filepath.Join(other, "deploy.yaml"), link); err != nil {
		t.Skip(err)
	}
	// Users get full control of the link, but not of the file that it
	// points to.
	name, _ := windows.UTF16PtrFromString(link)
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd(t, "D:(A;;FA;;;BU)(A;;FA;;;SY)(A;;FA;;;BA)").DACL()
	if err == nil {
		err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}
	windows.CloseHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(Path())
	for _, want := range []string{"the link " + link + " can be changed by ", "(S-1-5-32-545)", `icacls "` + link + `" /L /inheritance:r`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}
