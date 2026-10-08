package fileio

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"crude/logger"
)

func TestSessionsRootConfiguration(t *testing.T) {
	t.Cleanup(func() { SetSessionsRootFn(nil) })
	SetSessionsRootFn(nil)
	fallback, err := journalDirFallback()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := journalDir(); err != nil || got != fallback {
		t.Fatalf("default directory = %q, %v; want %q", got, err, fallback)
	}

	wantErr := errors.New("configured root unavailable")
	SetSessionsRootFn(func() (string, error) { return "", wantErr })
	if got, err := journalDir(); got != "" || err != wantErr {
		t.Fatalf("configured error = %q, %v; want original error", got, err)
	}
	if _, err := OpenJournal("unavailable", nil); !errors.Is(err, wantErr) {
		t.Fatalf("open journal error = %v; want configured error", err)
	}
	SetSessionsRootFn(func() (string, error) { return "", nil })
	if got, err := journalDir(); got != "" || err != nil {
		t.Fatalf("empty configured directory = %q, %v; must not fall back", got, err)
	}

	root := t.TempDir()
	calls := 0
	SetSessionsRootFn(func() (string, error) {
		calls++
		SetSessionsRootFn(nil)
		return root, nil
	})
	if got, err := journalDir(); got != root || err != nil || calls != 1 {
		t.Fatalf("callback snapshot = %q, %v, calls=%d; want %q once", got, err, calls, root)
	}
	if got, err := journalDir(); got != fallback || err != nil {
		t.Fatalf("reset directory = %q, %v; want %q", got, err, fallback)
	}
}

func TestJournalDefaultAndExplicitDirectories(t *testing.T) {
	t.Cleanup(func() { SetSessionsRootFn(nil) })
	first, second := t.TempDir(), t.TempDir()
	for _, root := range []string{first, second} {
		SetSessionsRootFn(func() (string, error) { return root, nil })
		journal, err := OpenJournal("default", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := journal.f.Name(), filepath.Join(root, "default.jsonl"); got != want {
			t.Errorf("default journal path = %q; want %q", got, want)
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}

		editor, err := NewEditorWithSessionDir("explicit", first, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := editor.journal.f.Name(), filepath.Join(first, "explicit.jsonl"); got != want {
			t.Errorf("explicit journal path = %q; want %q", got, want)
		}
		if err := editor.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSafetyLoggerConfiguration(t *testing.T) {
	t.Cleanup(func() { SetSafetyLogger(nil) })
	SetSafetyLogger(nil)
	safetyLogIf(func(*logger.Logger) { t.Error("nil logger invoked callback") })

	first, second := &logger.Logger{}, &logger.Logger{}
	SetSafetyLogger(first)
	calls := 0
	safetyLogIf(func(got *logger.Logger) {
		calls++
		SetSafetyLogger(second)
		if got != first {
			t.Error("callback did not retain its logger snapshot")
		}
	})
	safetyLogIf(func(got *logger.Logger) {
		calls++
		if got != second {
			t.Error("replacement logger was not used")
		}
		SetSafetyLogger(nil)
	})
	if calls != 2 {
		t.Fatalf("logger callbacks = %d; want 2", calls)
	}
	safetyLogIf(func(*logger.Logger) { t.Error("reset logger invoked callback") })
}

func TestGlobalConfigurationConcurrentSettersAndReaders(t *testing.T) {
	fallback, err := journalDirFallback()
	if err != nil {
		t.Fatal(err)
	}
	first, second := t.TempDir(), t.TempDir()
	wantErr := errors.New("configured root unavailable")
	roots := []func() (string, error){
		nil,
		func() (string, error) { return first, nil },
		func() (string, error) { return second, nil },
		func() (string, error) { return "", wantErr },
	}
	loggers := []*logger.Logger{nil}
	for _, id := range []string{"first", "second"} {
		lg, err := logger.New(logger.Options{SessionsDir: t.TempDir(), SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := lg.Close(); err != nil {
				t.Error(err)
			}
		})
		loggers = append(loggers, lg)
	}
	t.Cleanup(func() {
		SetSessionsRootFn(nil)
		SetSafetyLogger(nil)
	})
	SetSessionsRootFn(roots[1])
	SetSafetyLogger(loggers[1])

	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			<-start
			for iteration := range 512 {
				if worker%2 == 0 {
					SetSessionsRootFn(roots[(worker+iteration)%len(roots)])
					SetSafetyLogger(loggers[(worker+iteration)%len(loggers)])
					continue
				}
				got, err := journalDir()
				if err == nil {
					if got != fallback && got != first && got != second {
						t.Errorf("unexpected root snapshot %q", got)
					}
				} else if got != "" || err != wantErr {
					t.Errorf("unexpected root error snapshot %q, %v", got, err)
				}
				safetyLogIf(func(lg *logger.Logger) {
					if lg != loggers[1] && lg != loggers[2] {
						t.Error("unexpected logger snapshot")
						return
					}
					lg.Warn("SAFETY", "configuration_concurrency_probe")
				})
			}
		})
	}
	close(start)
	workers.Wait()

	SetSessionsRootFn(nil)
	SetSafetyLogger(nil)
	if got, err := journalDir(); got != fallback || err != nil {
		t.Fatalf("reset directory = %q, %v; want %q", got, err, fallback)
	}
	safetyLogIf(func(*logger.Logger) { t.Error("reset logger invoked callback") })
}
