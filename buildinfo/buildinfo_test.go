package buildinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeHonorsIsolatedProfile(t *testing.T) {
	t.Setenv(HomeEnv, "")
	real, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Home(); err != nil || got != real {
		t.Fatalf("default home = %q, %v; want %q", got, err, real)
	}
	profile := t.TempDir()
	t.Chdir(profile)
	t.Setenv(HomeEnv, "dev-profile")
	got, err := Home()
	if want := filepath.Join(profile, "dev-profile"); err != nil || got != want {
		t.Fatalf("isolated home = %q, %v; want absolute %q", got, err, want)
	}
	if user, _ := os.UserHomeDir(); user != real {
		t.Fatal("the profile override leaked into the real HOME seen by child tools")
	}
}
