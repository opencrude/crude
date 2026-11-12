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

func TestDevelopmentBuildSelectsItsOwnProfileAndDoesNotPassItOn(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	t.Setenv(HomeEnv, "")
	t.Cleanup(func() { selected.home, selected.development = "", false })

	home, err := SelectHome()
	if want := filepath.Join(user, DevelopmentProfile); err != nil || home != want || !UsesDevelopmentProfile() {
		t.Fatalf("development home = %q, %v, development=%v; want %q", home, err, UsesDevelopmentProfile(), want)
	}
	if got, _ := Home(); got != home {
		t.Fatalf("Home() = %q after selection; want %q", got, home)
	}

	explicit := t.TempDir()
	t.Setenv(HomeEnv, explicit)
	home, err = SelectHome()
	if err != nil || home != explicit || UsesDevelopmentProfile() {
		t.Fatalf("explicit home = %q, %v, development=%v; want %q", home, err, UsesDevelopmentProfile(), explicit)
	}
	if _, inherited := os.LookupEnv(HomeEnv); inherited {
		t.Fatal("programs started by Crude would inherit this process's profile")
	}
	if got, _ := Home(); got != explicit {
		t.Fatalf("Home() = %q once the variable is removed; want the selected %q", got, explicit)
	}
}
