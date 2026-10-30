package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const processEnvironmentSentinel = "TEST_ONLY_ENV_SENTINEL_7e09"

func requireProcessEnvironment(t *testing.T, values map[string]string) ProcessEnvironment {
	t.Helper()
	env, err := NewProcessEnvironment(values)
	if err != nil {
		t.Fatal(err)
	}
	if !env.prepared() {
		t.Fatal("successful constructor returned an unprepared environment")
	}
	return env
}

func requireProcessEnvironmentError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrProcessEnvironment) || err.Error() != "invalid process environment or executable" {
		t.Fatal("failure must return the bounded generic environment error")
	}
}

func processEnvironmentFixture(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("test-only executable lookup fixture\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func processEnvironmentExecutableName() string {
	if runtime.GOOS == "windows" {
		return "analyzer.exe"
	}
	return "analyzer"
}

func TestProcessEnvironmentExplicitEmptyIsNotUnpreparedOrInherited(t *testing.T) {
	t.Setenv("POWER_ENV_TEST_AMBIENT", processEnvironmentSentinel)
	var zero ProcessEnvironment
	if zero.prepared() || zero.Env() != nil {
		t.Fatal("zero environment became prepared")
	}
	for _, values := range []map[string]string{nil, {}} {
		env := requireProcessEnvironment(t, values)
		if got := env.Env(); got == nil || len(got) != 0 {
			t.Fatal("prepared empty environment must be nonnil and contain no ambient entries")
		}
		if _, err := env.resolve("analyzer"); err == nil {
			t.Fatal("empty environment searched an implicit PATH")
		}
	}
	executable := processEnvironmentFixture(t, t.TempDir(), processEnvironmentExecutableName(), 0700)
	if _, err := zero.resolve(executable); err == nil {
		t.Fatal("unprepared environment accepted an absolute executable")
	} else {
		requireProcessEnvironmentError(t, err)
	}
	empty := requireProcessEnvironment(t, nil)
	if got, err := empty.resolve(executable); err != nil || got != executable {
		t.Fatal("explicit absolute executable must not need PATH")
	}
}

func TestProcessEnvironmentCompleteSortedDetachedAndConcurrent(t *testing.T) {
	dir := t.TempDir()
	executable := processEnvironmentFixture(t, dir, processEnvironmentExecutableName(), 0700)
	values := map[string]string{
		"Z": processEnvironmentSentinel, "PATH": dir,
		"A": "", "ProgramFiles(x86)": "test-only directory", "_x.y-z": "雪\n=",
	}
	env := requireProcessEnvironment(t, values)
	want := []string{
		"A=", "PATH=" + dir, "ProgramFiles(x86)=test-only directory",
		"Z=" + processEnvironmentSentinel, "_x.y-z=雪\n=",
	}
	if !reflect.DeepEqual(env.Env(), want) {
		t.Fatal("complete explicit launch data was changed or not sorted")
	}
	values["PATH"] = "not-an-absolute-directory"
	values["Z"] = "changed"
	delete(values, "A")
	copyValue := env
	got := env.Env()
	got[0] = "MUTATED=yes"
	got = append(got, "ADDED=yes")
	if len(got) != 6 || !reflect.DeepEqual(copyValue.Env(), want) {
		t.Fatal("caller mutation reached immutable storage")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				out := copyValue.Env()
				if !reflect.DeepEqual(out, want) {
					t.Error("concurrent environment changed")
					return
				}
				out[0] = "PRIVATE_COPY=yes"
				if resolved, err := copyValue.resolve(processEnvironmentExecutableName()); err != nil || resolved != executable {
					t.Error("concurrent explicit lookup changed")
					return
				}
				if strings.Contains(fmt.Sprintf("%#v", copyValue), processEnvironmentSentinel) {
					t.Error("concurrent formatting exposed a value")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestProcessEnvironmentRejectsMalformedKeysAndValues(t *testing.T) {
	for _, key := range []string{"", "A=B", "A\x00B", "A\nB", "A\tB", "A\x7fB", "é", "A B", "=C:", "1ABC", ".A", strings.Repeat("A", 257)} {
		env, err := NewProcessEnvironment(map[string]string{key: processEnvironmentSentinel})
		requireProcessEnvironmentError(t, err)
		if env.prepared() {
			t.Fatal("invalid key left a prepared environment")
		}
	}
	for _, value := range []string{"before\x00after", "\xff", strings.Repeat("x", 32769)} {
		env, err := NewProcessEnvironment(map[string]string{"TEST_VALUE": value})
		requireProcessEnvironmentError(t, err)
		if env.prepared() {
			t.Fatal("invalid value left a prepared environment")
		}
	}
	requireProcessEnvironment(t, map[string]string{strings.Repeat("A", 256): strings.Repeat("x", 32768)})
	values := map[string]string{"Alpha": "one", "ALPHA": "two"}
	env, err := NewProcessEnvironment(values)
	if runtime.GOOS == "windows" {
		requireProcessEnvironmentError(t, err)
	} else if err != nil || len(env.Env()) != 2 {
		t.Fatal("Unix environment keys must remain case-sensitive")
	}
}

func TestProcessEnvironmentLiteralCapacityBoundaries(t *testing.T) {
	values := make(map[string]string)
	for i := range 256 {
		values[fmt.Sprintf("KEY_%03d", i)] = ""
	}
	requireProcessEnvironment(t, values)
	values["ONE_TOO_MANY"] = ""
	_, err := NewProcessEnvironment(values)
	requireProcessEnvironmentError(t, err)
	// 4 single-byte keys, '=' and NUL cost 12 bytes; values sum to
	// 131060 bytes. The complete explicit table is exactly 128 KiB.
	values = map[string]string{
		"A": strings.Repeat("x", 32768), "B": strings.Repeat("x", 32768),
		"C": strings.Repeat("x", 32768), "D": strings.Repeat("x", 32756),
	}
	requireProcessEnvironment(t, values)
	values["D"] += "x"
	_, err = NewProcessEnvironment(values)
	requireProcessEnvironmentError(t, err)
}

func TestProcessEnvironmentPATHRejectsHiddenSearchAndBounds(t *testing.T) {
	dir := t.TempDir()
	separator := string(os.PathListSeparator)
	unsafe := []string{
		"", ".", "..", "relative", "." + separator + dir,
		separator + dir, dir + separator, dir + separator + separator + dir,
		filepath.VolumeName(dir) + string(os.PathSeparator) + "a/../b",
		dir + string(os.PathSeparator) + ".", dir + "\x00suffix", dir + "\n",
		dir + "\x7f", dir + "\u0085", dir + "\xff",
		strings.Join(make([]string, 65), separator),
	}
	for _, path := range unsafe {
		_, err := NewProcessEnvironment(map[string]string{"PATH": path})
		requireProcessEnvironmentError(t, err)
	}
	// The input bound applies even to repeated entries which would deduplicate.
	repeated := make([]string, 64)
	for i := range repeated {
		repeated[i] = dir
	}
	env := requireProcessEnvironment(t, map[string]string{"PATH": strings.Join(repeated, separator)})
	if !reflect.DeepEqual(env.Env(), []string{"PATH=" + dir}) {
		t.Fatal("exact duplicates not removed from privileged launch data")
	}
	repeated = append(repeated, dir)
	_, err := NewProcessEnvironment(map[string]string{"PATH": strings.Join(repeated, separator)})
	requireProcessEnvironmentError(t, err)
	prefix := string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		prefix = `C:\`
	}
	long := prefix + strings.Repeat("x", 4096-len(prefix))
	requireProcessEnvironment(t, map[string]string{"PATH": long})
	_, err = NewProcessEnvironment(map[string]string{"PATH": long + "x"})
	requireProcessEnvironmentError(t, err)
	// Preparation is lexical: missing directories and ordinary files do not
	// cause an eager stat or invalidate the complete environment.
	requireProcessEnvironment(t, map[string]string{"PATH": filepath.Join(dir, "absent")})
	file := processEnvironmentFixture(t, dir, "not-a-directory", 0600)
	requireProcessEnvironment(t, map[string]string{"PATH": file})
}

func TestProcessEnvironmentLookupUsesConfiguredNotAmbientPATH(t *testing.T) {
	first, second, ambient := t.TempDir(), t.TempDir(), t.TempDir()
	name := processEnvironmentExecutableName()
	firstExe := processEnvironmentFixture(t, first, name, 0700)
	secondExe := processEnvironmentFixture(t, second, name, 0700)
	processEnvironmentFixture(t, ambient, name, 0700)
	t.Setenv("PATH", ambient)
	path := first + string(os.PathListSeparator) + second + string(os.PathListSeparator) + first
	values := map[string]string{"PATH": path}
	env := requireProcessEnvironment(t, values)
	if !reflect.DeepEqual(env.Env(), []string{"PATH=" + first + string(os.PathListSeparator) + second}) {
		t.Fatal("PATH deduplication changed first-occurrence order")
	}
	values["PATH"] = ambient
	got, err := env.resolve(name)
	if err != nil || got != firstExe {
		t.Fatal("lookup ignored configured PATH order or used ambient/caller-mutated PATH")
	}
	if err := os.Remove(firstExe); err != nil {
		t.Fatal(err)
	}
	if got, err = env.resolve(name); err != nil || got != secondExe {
		t.Fatal("lookup failed to observe the second explicit directory after removal")
	}
	if err := os.Remove(secondExe); err != nil {
		t.Fatal(err)
	}
	if _, err = env.resolve(name); err == nil {
		t.Fatal("lookup fell back to the ambient executable")
	}
	t.Chdir(ambient)
	empty := requireProcessEnvironment(t, nil)
	if _, err := empty.resolve(name); err == nil {
		t.Fatal("lookup searched the current working directory")
	}
}

func TestProcessEnvironmentLookupNegativeAndObservedSymlinks(t *testing.T) {
	dir := t.TempDir()
	name := processEnvironmentExecutableName()
	env := requireProcessEnvironment(t, map[string]string{"PATH": dir})
	for _, command := range []string{"", ".", "..", "./" + name, "../" + name, "dir/" + name, `dir\` + name, "C:" + name, "a\x00b", "a\nb", "\xff", strings.Repeat("x", 4097)} {
		_, err := env.resolve(command)
		requireProcessEnvironmentError(t, err)
	}
	directory := filepath.Join(dir, name)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{name, directory, filepath.Join(dir, "missing")} {
		_, err := env.resolve(command)
		requireProcessEnvironmentError(t, err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	executable := processEnvironmentFixture(t, dir, name, 0600)
	if runtime.GOOS == "windows" {
		// Windows mode bits are not an execute-access or PE-validity check.
		if got, err := env.resolve(name); err != nil || got != executable {
			t.Fatal("Windows regular EXE fixture not resolved")
		}
		return
	}
	_, err := env.resolve(name)
	requireProcessEnvironmentError(t, err)
	if err := os.Chmod(executable, 0100); err != nil {
		t.Fatal(err)
	}
	if got, err := env.resolve(name); err != nil || got != executable {
		t.Fatal("Unix execute mode not recognized")
	}
	link := filepath.Join(dir, "linked-analyzer")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	if got, err := env.resolve(link); err != nil || got != link {
		t.Fatal("lookup must report its observed symlink spelling, not claim canonical identity")
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	_, err = env.resolve(link)
	requireProcessEnvironmentError(t, err)
	if err := os.Mkdir(executable, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = env.resolve(link)
	requireProcessEnvironmentError(t, err)
}

func TestProcessEnvironmentValuesNeverAppearInFormattingJSONOrErrors(t *testing.T) {
	env := requireProcessEnvironment(t, map[string]string{"PRIVATE_TEST_KEY": processEnvironmentSentinel})
	wrapper := struct{ Environment ProcessEnvironment }{env}
	for _, value := range []any{env, &env, wrapper, []ProcessEnvironment{env}, map[string]ProcessEnvironment{"launch": env}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%#08v"} {
			got := fmt.Sprintf(format, value)
			if strings.Contains(got, processEnvironmentSentinel) || strings.Contains(got, "PRIVATE_TEST_KEY") {
				t.Fatal("normal formatting exposed environment contents")
			}
		}
		data, err := json.Marshal(value)
		if err != nil || strings.Contains(string(data), processEnvironmentSentinel) || strings.Contains(string(data), "PRIVATE_TEST_KEY") {
			t.Fatal("JSON serialization failed or exposed environment contents")
		}
	}
	data, err := json.Marshal(env)
	if err != nil || string(data) != `"ProcessEnvironment([redacted])"` {
		t.Fatal("unexpected redacted serialization contract")
	}
	_, err = NewProcessEnvironment(map[string]string{"INVALID=" + processEnvironmentSentinel: processEnvironmentSentinel})
	requireProcessEnvironmentError(t, err)
	_, err = env.resolve(filepath.Join(t.TempDir(), processEnvironmentSentinel))
	requireProcessEnvironmentError(t, err)
	_, err = env.resolve(processEnvironmentSentinel + "\x00")
	requireProcessEnvironmentError(t, err)
	// Only the explicit privileged launch accessor exposes a supplied value.
	if !reflect.DeepEqual(env.Env(), []string{"PRIVATE_TEST_KEY=" + processEnvironmentSentinel}) {
		t.Fatal("redaction corrupted launch data")
	}
}

func TestProcessEnvironmentWindowsNativePATHAndExtensionRules(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows path and filesystem semantics")
	}
	for _, path := range []string{
		`C:relative`, `\rooted`, `/rooted`, `\\?\C:\tools`, `\\.\C:\tools`,
		`\\server`, `\\server\`, `C:\tools:stream`, `C:\tools.`,
		`C:\tools `, `C:\bad?name`, `C:\a\..\b`,
	} {
		_, err := NewProcessEnvironment(map[string]string{"Path": path})
		requireProcessEnvironmentError(t, err)
	}
	for _, path := range []string{`C:\`, `C:\Program Files\tools`, `\\server\share\tools`} {
		env := requireProcessEnvironment(t, map[string]string{"Path": path})
		if !reflect.DeepEqual(env.Env(), []string{"Path=" + path}) {
			t.Fatal("native path spelling was unexpectedly changed")
		}
	}
	for _, extension := range []string{"", "EXE", ".EXE;", ".EXE;;.COM", ".", ".E/XE", ".E XE", ".EXE:.COM", strings.Repeat(".EXE;", 16) + ".COM", "." + strings.Repeat("a", 32)} {
		_, err := NewProcessEnvironment(map[string]string{"PATHEXT": extension})
		requireProcessEnvironmentError(t, err)
	}
	dir, ambient := t.TempDir(), t.TempDir()
	exe := processEnvironmentFixture(t, dir, "tool.exe", 0600)
	com := processEnvironmentFixture(t, dir, "tool.com", 0600)
	cmd := processEnvironmentFixture(t, dir, "tool.cmd", 0600)
	processEnvironmentFixture(t, dir, "tool.bat", 0600)
	processEnvironmentFixture(t, ambient, "tool.exe", 0600)
	t.Setenv("PATH", ambient)
	t.Setenv("PATHEXT", ".EXE")
	env := requireProcessEnvironment(t, map[string]string{"Path": dir})
	_, err := env.resolve("tool")
	requireProcessEnvironmentError(t, err)
	if got, err := env.resolve("tool.exe"); err != nil || got != exe {
		t.Fatal("explicit native extension required ambient PATHEXT")
	}
	env = requireProcessEnvironment(t, map[string]string{"Path": dir, "PathExt": ".CMD;.COM;.EXE;.com;.BAT"})
	if got, err := env.resolve("tool"); err != nil || got != com {
		t.Fatal("explicit PATHEXT order/deduplication or shell exclusion failed")
	}
	empty := requireProcessEnvironment(t, nil)
	if got, err := empty.resolve(exe); err != nil || got != exe {
		t.Fatal("absolute EXE required implicit extensions")
	}
	for _, command := range []string{"tool.cmd", "tool.bat", cmd, strings.TrimSuffix(exe, ".exe"), "tool.exe:stream", "tool."} {
		_, err := env.resolve(command)
		requireProcessEnvironmentError(t, err)
	}
	// A missing explicitly configured native candidate cannot fall back to EXE.
	env = requireProcessEnvironment(t, map[string]string{"PATH": dir, "PATHEXT": ".CMD"})
	_, err = env.resolve("tool")
	requireProcessEnvironmentError(t, err)
	env = requireProcessEnvironment(t, map[string]string{"PATH": dir, "PATHEXT": ".EXE"})
	if got, err := env.resolve("tool"); err != nil || got != exe {
		t.Fatal("explicit extension lookup failed")
	}
}
