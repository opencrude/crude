package app

import (
	"os"
	"path/filepath"
	"testing"

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
	if windows.EqualSid(ownerSIDOf(t, path), user.User.Sid) {
		t.Skip("this logon already creates files owned by the current user")
	}
	if err := secureOwnerPath(path, false); err != nil {
		t.Fatalf("securing a path created by this logon failed: %v", err)
	}
	if owner := ownerSIDOf(t, path); !windows.EqualSid(owner, user.User.Sid) {
		t.Fatalf("owner was not claimed for the current user: %s", owner.String())
	}
}
