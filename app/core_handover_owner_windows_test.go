package app

import (
	"os"
	"path/filepath"
	"testing"

	"crude/internal/testsymlink"
	"crude/types"

	"golang.org/x/sys/windows"
)

func ownerSIDOf(t *testing.T, path string) *windows.SID {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		t.Fatalf("read owner of %s: %v", path, err)
	}
	return owner
}

// A token whose default owner is not the current user (an elevated or network
// logon) creates files owned by that identity even when their DACL is private.
// Securing a core owner path must claim it for the user, or no core can start
// from that logon and handover can never stop the one that already runs.
func TestSecureOwnerPathClaimsOwnershipFromDefaultTokenOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifetime.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := secureOwnerPath(path, false); err != nil {
		t.Fatalf("securing a path created by this logon failed: %v", err)
	}
	if owner := ownerSIDOf(t, path); !windows.EqualSid(owner, user.User.Sid) {
		t.Fatalf("owner was not claimed for the current user: %s", owner.String())
	}
}

func TestCoreOwnerWindowsRejectsPublicDACL(t *testing.T) {
	home := coreTestHome(t)
	lock, err := lockCoreLifetime(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if _, err := installCoreOwner(home); err != nil {
		t.Fatal(err)
	}
	path := coreOwnerPath(home)
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readCoreOwner(home); err == nil {
		t.Fatal("descriptor granting Everyone access was accepted")
	}
}

func TestCoreOwnerWindowsRejectsSymlinkDescriptor(t *testing.T) {
	home := coreTestHome(t)
	lock, err := lockCoreLifetime(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	owner, err := installCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	path := coreOwnerPath(home)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	testsymlink.Create(t, path+".saved", path)
	if _, err := readCoreOwner(home); err == nil {
		t.Fatal("symlink descriptor accepted")
	}
	if err := removeCoreOwner(home, owner.Generation); err == nil {
		t.Fatal("unsafe symlink descriptor removed")
	}
}

func TestStartupOwnerWindowsDeniedDirectorySave(t *testing.T) {
	home := startupOwnerHome(t)
	a := newStartupOwner(t, home)
	writeStartupConfig(t, `{}`)
	parent := filepath.Dir(ConfigPath())
	original, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	oldDACL, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;0x00000002;;;" + sid + ")(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, oldDACL, nil); err != nil {
			t.Errorf("restore directory ACL: %v", err)
		}
	})
	if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{startupChoice("introduction_completed", "true")}); err == nil {
		t.Fatal("denied configuration save reported success")
	}
	if a.committedDefaults().Onboarding.IntroductionCompleted {
		t.Fatal("denied save published completion in memory")
	}
	if raw, err := os.ReadFile(ConfigPath()); err != nil || string(raw) != `{}` {
		t.Fatalf("denied save changed config: %q, %v", raw, err)
	}
}
