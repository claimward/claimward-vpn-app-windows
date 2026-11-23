package scripts

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func installText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Audit 2026-11-23: install.ps1 accepted an http:// server and wrote it into
// the helper's configuration, so the person's bearer token travelled in clear.
// The structural half runs everywhere: the check is https-only and sits before
// the first thing the installer changes.
func TestInstallJudgesServersBeforeChangingAnything(t *testing.T) {
	s := installText(t)
	if strings.Contains(s, "-ne 'http'") {
		t.Error("install.ps1 still accepts the http scheme")
	}
	check := strings.Index(s, "-Server must be an https URL")
	if check < 0 {
		t.Fatal("install.ps1 has no https-only check")
	}
	if !strings.Contains(s[:check], "$u.Scheme -ne 'https'") {
		t.Error("the check does not test the scheme against https")
	}
	for _, change := range []string{"Stop-Service", "New-Item ", "Copy-Item", "New-LocalGroup", "WriteAllText"} {
		if i := strings.Index(s, change); i >= 0 && i < check {
			t.Errorf("%q comes before the server check", change)
		}
	}
}

// Audit 2026-11-23: C:\ProgramData is writable by every user; a directory
// pre-created there by a local user stayed theirs after the installer re-ACLed
// it. The data directory's owner is judged before and after it is created, and
// before its ACL is replaced.
func TestInstallChecksTheOwnerOfTheDataDirectory(t *testing.T) {
	s := installText(t)
	for _, want := range []string{"S-1-5-18", "S-1-5-32-544", "ReparsePoint", "GetOwner("} {
		if !strings.Contains(s, want) {
			t.Errorf("install.ps1 lacks %q", want)
		}
	}
	first := strings.Index(s, "Assert-TrustedOwner $DataDir")
	mk := strings.Index(s, "New-Item -ItemType Directory -Force -Path $DataDir")
	acl := strings.Index(s, "Set-Sddl $DataDir")
	if first < 0 || mk < 0 || acl < 0 {
		t.Fatalf("missing steps: check %d, create %d, acl %d", first, mk, acl)
	}
	if !(first < mk && mk < acl) {
		t.Errorf("order wrong: check %d, create %d, acl %d", first, mk, acl)
	}
	if strings.Count(s[mk:acl], "Assert-TrustedOwner $DataDir") != 1 {
		t.Error("the owner is not judged again between creating the directory and replacing its ACL")
	}
}

// Where Windows PowerShell is present and elevated, run the real refusal.
func TestInstallRefusesAnHTTPServerWhenRun(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the script reads Windows environment variables (ProgramFiles) before it can run")
	}
	ps, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("no Windows PowerShell")
	}
	out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-File", "install.ps1", "-Server", "http://vpn.example.com").CombinedOutput()
	s := string(out)
	if err == nil {
		t.Fatalf("an http server was accepted: %s", s)
	}
	if strings.Contains(s, "RunAsAdministrator") || strings.Contains(s, "administrator privileges") {
		t.Skip("not elevated: the #Requires line stops it first")
	}
	if !strings.Contains(s, "must be an https URL") {
		t.Errorf("unexpected refusal: %s", s)
	}
}
