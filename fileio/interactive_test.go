//go:build !windows

package fileio

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInteractiveExecPipesAndPTY(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "pipes", true: "pty"}[tty], func(t *testing.T) {
			mgr := NewAsyncCommandManager()
			defer mgr.CloseAndWait()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := mgr.StartInteractive(ctx, InteractiveOptions{Command: "printf ready; read x; printf 'reply:%s' \"$x\"", Shell: "/bin/sh", TTY: tty}, 50*time.Millisecond, 1000)
			if err != nil || result.SessionID == 0 || !strings.Contains(result.Output, "ready") {
				t.Fatalf("start %+v %v", result, err)
			}
			result, err = mgr.WriteInteractive(ctx, result.SessionID, "hello\n", time.Second, 1000)
			if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Output, "reply:hello") {
				t.Fatalf("continue %+v %v", result, err)
			}
		})
	}
}

func TestInteractiveExecCancellationAndShutdown(t *testing.T) {
	mgr := NewAsyncCommandManager()
	result, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: "sleep 60", Shell: "/bin/sh"}, time.Millisecond, 1000)
	if err != nil || result.SessionID == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := mgr.WriteInteractive(ctx, result.SessionID, "", time.Second, 1000); err == nil {
		t.Fatal("cancel not observed")
	}
	mgr.CloseAndWait()
	if mgr.Running() {
		t.Fatal("process survived close")
	}
	if _, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: "echo no"}, time.Millisecond, 10); err == nil {
		t.Fatal("closed manager launched a process")
	}
}

func TestInteractiveExecRetainsTruncatedOutputWithExactRetrieval(t *testing.T) {
	mgr := NewAsyncCommandManager()
	defer mgr.CloseAndWait()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := mgr.StartInteractive(ctx, InteractiveOptions{
		Command: "printf '%05000d' 0",
		Shell:   "/bin/sh",
	}, time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 || result.OriginalTokenCount == 0 {
		t.Fatalf("terminal result=%+v", result)
	}
	d := result.OutputDescriptor
	if d.Handle == "" || d.Status != AsyncCompleted || d.TotalBytes != 5000 || d.OmittedBytes <= 0 || len(d.ShownRanges) != 2 {
		t.Fatalf("descriptor=%+v", d)
	}
	if !strings.Contains(result.Retrieval, `read_async_output(handle="`+d.Handle+`"`) || !strings.Contains(result.Retrieval, "do not rerun") {
		t.Fatalf("retrieval=%q", result.Retrieval)
	}
	page, err := mgr.ReadOutputPage(d.Handle, d.ShownRanges[0].EndByte, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Output) != 128 || page.NextByte != d.ShownRanges[0].EndByte+128 || page.Descriptor.Status != AsyncCompleted {
		t.Fatalf("page=%+v", page)
	}
}
