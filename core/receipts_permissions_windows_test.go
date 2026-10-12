package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func setReceiptDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
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
}

func makeReceiptPublic(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	setReceiptDACL(t, path, fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FR;;;WD)", user.User.Sid.String()))
}

func TestReceiptWindowsAccessControl(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, extra string
		private     bool
	}{
		{"user-only", "", true},
		{"system-and-administrators", "(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"everyone-readable", "(A;;FR;;;WD)", false},
		{"authenticated-users-writable", "(A;;FW;;;AU)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			setReceiptDACL(t, path, fmt.Sprintf("D:P(A;;FA;;;%s)%s", user.User.Sid.String(), tc.extra))
			if err := privatePath(path); (err == nil) != tc.private {
				t.Fatalf("private=%t: %v", tc.private, err)
			}
		})
	}
}

func TestReceiptWindowsRejectsUnrestrictedAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	setReceiptDACL(t, path, "D:NO_ACCESS_CONTROL")
	if err := privatePath(path); err == nil {
		t.Fatal("unrestricted Windows access accepted")
	}
}

func TestReceiptWindowsRejectsPublicInheritedFiles(t *testing.T) {
	parent := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	setReceiptDACL(t, parent, fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FR;;;WD)", user.User.Sid.String()))
	if journal, err := openReceipts(filepath.Join(parent, "receipts.db")); err == nil {
		_ = journal.close()
		t.Fatal("new journal with public inherited permissions accepted")
	}
}
