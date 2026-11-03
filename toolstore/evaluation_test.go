package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStagedEvaluationPrecedesPublicationAndRetainsUncertainty(t *testing.T) {
	for _, mode := range []string{"passed", "failed", "cancelled", "unverified", "panic"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			server := serve(t, map[string][]byte{"/tool": []byte("real extracted bytes")})
			req := request(server.URL+"/tool", ArchiveRaw, "tool")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var staged Tool
			req.Evaluate = func(ctx context.Context, tool Tool) error {
				staged = tool
				if !store.mu.TryLock() {
					t.Fatal("evaluation ran under the store lock")
				}
				store.mu.Unlock()
				if _, ok := store.Lookup(req.Package, req.Version); ok {
					t.Fatal("staged tool was already resolvable")
				}
				if got := readFile(t, tool.Executable); got != "real extracted bytes" {
					t.Fatalf("staged bytes: %q", got)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(tool.Dir), evaluationPendingName)); err != nil {
					t.Fatalf("pre-launch uncertainty marker is missing: %v", err)
				}
				switch mode {
				case "failed":
					return errors.New("initialize failed")
				case "cancelled":
					cancel()
					return ctx.Err()
				case "unverified":
					return &EvaluationUnverifiedError{Err: errors.New("Stop could not join")}
				case "panic":
					panic("interrupted evaluator")
				}
				return nil
			}
			var tool Tool
			var err error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				tool, err = store.Install(ctx, req)
			}()
			if staged.Executable == "" {
				t.Fatal("the real extraction did not reach evaluation")
			}
			if mode == "passed" {
				if err != nil || tool.ID != staged.ID || tool.Executable == staged.Executable {
					t.Fatalf("publication after check: %+v %v", tool, err)
				}
				requireEmptyStaging(t, store)
				return
			}
			if mode == "panic" && recovered == nil || mode != "panic" && err == nil {
				t.Fatalf("failed check succeeded: error=%v panic=%v", err, recovered)
			}
			if _, ok := store.Lookup(req.Package, req.Version); ok {
				t.Fatal("failed check published the executable")
			}
			reopened, err := Open(store.root)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := reopened.Lookup(req.Package, req.Version); ok {
				t.Fatal("reopen adopted failed check bytes")
			}
			if mode == "unverified" || mode == "panic" {
				if _, err := os.Stat(staged.Executable); err != nil {
					t.Fatalf("uncertain process lost its exact bytes: %v", err)
				}
				unknown, err := reopened.Unrecognized(t.Context())
				if err != nil || len(unknown) != 1 {
					t.Fatalf("retained check not inspectable: %v %v", unknown, err)
				}
			} else {
				requireEmptyStaging(t, reopened)
			}
		})
	}
}
