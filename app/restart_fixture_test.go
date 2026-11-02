package app

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const restartFixtureMagic = "crude-restart-fixture-v1"

// The executable loader ignores this bounded trailer, including after staging.
func writeRestartFixture(t *testing.T, dir, behavior string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	path := filepath.Join(dir, "restart-fixture.exe")
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(dst, src); err == nil {
		_, err = fmt.Fprintf(dst, "%s%s", restartFixtureMagic, behavior)
	}
	if err == nil {
		err = binary.Write(dst, binary.BigEndian, uint32(len(behavior)))
	}
	if err == nil {
		_, err = io.WriteString(dst, restartFixtureMagic)
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func runRestartFixture() (int, bool) {
	if len(os.Args) < 2 || (os.Args[1] != "--core-handover-identity" && os.Args[1] != "--core-stop" && os.Args[1] != "--core-handover") {
		return 0, false
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, false
	}
	f, err := os.Open(exe)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	tail := make([]byte, 4+len(restartFixtureMagic))
	if err != nil || info.Size() < int64(len(tail)) {
		return 0, false
	}
	if _, err = f.ReadAt(tail, info.Size()-int64(len(tail))); err != nil || string(tail[4:]) != restartFixtureMagic {
		return 0, false
	}
	n := binary.BigEndian.Uint32(tail[:4])
	start := info.Size() - int64(len(tail)) - int64(n) - int64(len(restartFixtureMagic))
	if n > 256 || start < 0 {
		return 0, false
	}
	data := make([]byte, len(restartFixtureMagic)+int(n))
	if _, err = f.ReadAt(data, start); err != nil || string(data[:len(restartFixtureMagic)]) != restartFixtureMagic {
		return 0, false
	}
	fn, ok := restartFixtureBehaviors[string(data[len(restartFixtureMagic):])]
	if !ok {
		return 74, true
	}
	return fn(), true
}

var restartFixtureBehaviors = map[string]func() int{
	"self-digest": func() int {
		if len(os.Args) != 2 || os.Args[1] != "--core-handover-identity" || !probeEnvironmentEmpty(os.Environ()) {
			return 42
		}
		exe, err := os.Executable()
		if err != nil {
			return 43
		}
		digest, _, err := restartImageDigest(exe)
		if err != nil {
			return 43
		}
		fmt.Printf(`{"build_id":"%s","cohort":"%s","schema":"new-schema","sha256":"%s","goos":"%s","goarch":"%s"}`, digest[:16], strings.Repeat("3", 64), digest, runtime.GOOS, runtime.GOARCH)
		return 0
	},
	"valid":            func() int { return fixtureIdentity("valid") },
	"unknown-field":    func() int { return fixtureIdentity("unknown-field") },
	"duplicate-key":    func() int { return fixtureIdentity("duplicate-key") },
	"trailing-value":   func() int { return fixtureIdentity("trailing-value") },
	"oversized":        func() int { return fixtureIdentity("oversized") },
	"stderr-oversized": func() int { return fixtureIdentity("stderr-oversized") },
	"exit":             func() int { fmt.Fprintln(os.Stderr, "private-secret-error"); return 73 },
	"cancelled": func() int {
		for {
			runtime.Gosched()
		}
	},
	"exact-owner":           func() int { return fixtureStop("exact-owner") },
	"refused-then-accepted": func() int { return fixtureStop("refused-then-accepted") },
	"unknown-failure":       func() int { return fixtureStop("unknown-failure") },
	"panic-exit-two":        func() int { return fixtureStop("panic-exit-two") },
	"permanent-refusal":     func() int { return fixtureStop("permanent-refusal") },
	"replacement-race":      func() int { return fixtureStop("replacement-race") },
	"successor": func() int {
		if len(os.Args) != 3 || os.Args[1] != "--core-handover" || os.Getenv("CRUDE_RESTART_JOIN_FIXTURE") != "one" {
			return 51
		}
		cwd, err := os.Getwd()
		if err != nil || !strings.EqualFold(cwd, filepath.Dir(os.Args[2])) {
			return 52
		}
		if os.WriteFile(os.Args[2], []byte("foreground child completed"), 0600) != nil {
			return 53
		}
		return 0
	},
}

// The identity probe passes an empty environment. On Windows, os/exec always
// adds SYSTEMROOT to an explicit environment because system DLLs need it; that
// single variable is the only one a Windows probe may receive.
func probeEnvironmentEmpty(env []string) bool {
	if runtime.GOOS == "windows" && len(env) == 1 {
		return strings.HasPrefix(strings.ToUpper(env[0]), "SYSTEMROOT=")
	}
	return len(env) == 0
}

func fixtureIdentity(kind string) int {
	if len(os.Args) != 2 || os.Args[1] != "--core-handover-identity" {
		return 41
	}
	valid := fmt.Sprintf(`{"build_id":"0123456789abcdef","cohort":"%s","schema":"schema","sha256":"%s","goos":"%s","goarch":"%s"}`, strings.Repeat("2", 64), strings.Repeat("1", 64), runtime.GOOS, runtime.GOARCH)
	switch kind {
	case "unknown-field":
		valid = strings.TrimSuffix(valid, "}") + `,"unexpected":true}`
	case "duplicate-key":
		valid = strings.TrimSuffix(valid, "}") + `,"schema":"another"}`
	case "trailing-value":
		valid += "{}"
	case "oversized":
		valid = strings.Repeat("x", 9000)
	case "stderr-oversized":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 9000))
	}
	fmt.Print(valid)
	return 0
}

func fixtureStop(kind string) int {
	if len(os.Args) != 2 || os.Args[1] != "--core-stop" {
		return 61
	}
	home := os.Getenv("CRUDE_HOME")
	if kind == "exact-owner" {
		if os.Getenv("CRUDE_EXPECT_CORE_OWNER") != strings.Repeat("1", 64) {
			return 62
		}
		if os.WriteFile(filepath.Join(home, "harmless-helper-ran"), []byte("exact original helper"), 0600) != nil || os.Remove(coreOwnerPath(home)) != nil {
			return 63
		}
		return 0
	}
	f, err := os.OpenFile(filepath.Join(home, "attempts"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 64
	}
	_, err = f.WriteString("attempt\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 64
	}
	switch kind {
	case "refused-then-accepted":
		marker := filepath.Join(home, "first-refusal")
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if os.WriteFile(marker, nil, 0600) != nil {
				return 65
			}
			return coreHandoverRefusedExit
		}
		if os.Remove(coreOwnerPath(home)) != nil {
			return 65
		}
		return 0
	case "unknown-failure":
		return 1
	case "panic-exit-two":
		fmt.Fprintln(os.Stderr, "panic: uncertain result")
		return 2
	case "replacement-race":
		if os.WriteFile(filepath.Join(home, "refused"), nil, 0600) != nil {
			return 65
		}
		for {
			if _, err := os.Stat(filepath.Join(home, "replacement-published")); err == nil {
				break
			}
			runtime.Gosched()
		}
	}
	return coreHandoverRefusedExit
}
