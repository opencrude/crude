package fileio

import (
	"sync"
	"testing"

	"crude/logger"
)

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

func TestSafetyLoggerConcurrentSettersAndReaders(t *testing.T) {
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
	t.Cleanup(func() { SetSafetyLogger(nil) })
	SetSafetyLogger(loggers[1])

	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			<-start
			for iteration := range 512 {
				if worker%2 == 0 {
					SetSafetyLogger(loggers[(worker+iteration)%len(loggers)])
					continue
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

	SetSafetyLogger(nil)
	safetyLogIf(func(*logger.Logger) { t.Error("reset logger invoked callback") })
}
