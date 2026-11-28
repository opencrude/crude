//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// recorder owns the cast file and the pseudo console input.
type recorder struct {
	start  time.Time
	mu     sync.Mutex
	out    *bufio.Writer
	in     *os.File
	inject string
}

// event appends one timed asciicast event.
func (r *recorder) event(kind, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.Marshal([]any{time.Since(r.start).Seconds(), kind, data})
	if err == nil {
		_, err = r.out.Write(append(b, '\n'))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "castrec: write cast:", err)
	}
}

func (r *recorder) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.out.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "castrec: flush cast:", err)
	}
}

// send writes input bytes to the program.
func (r *recorder) send(s string) {
	if _, err := r.in.WriteString(s); err != nil {
		fmt.Fprintln(os.Stderr, "castrec: send input:", err)
	}
}

func main() {
	cols := flag.Int("cols", 160, "terminal columns")
	rows := flag.Int("rows", 45, "terminal rows")
	cwd := flag.String("cwd", "", "working directory of the program")
	out := flag.String("out", "rec.cast", "asciicast file to write")
	script := flag.String("script", "", "step script to run")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: castrec [flags] -- program [args...]")
		os.Exit(2)
	}
	if err := run(*cols, *rows, *cwd, *out, *script, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "castrec:", err)
		os.Exit(1)
	}
}

func run(cols, rows int, cwd, outPath, script string, args []string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	r := &recorder{out: bufio.NewWriterSize(f, 1<<20), inject: outPath + ".inject"}
	hdr, err := json.Marshal(map[string]any{"version": 2, "width": cols, "height": rows, "timestamp": time.Now().Unix()})
	if err != nil {
		return err
	}
	if _, err := r.out.Write(append(hdr, '\n')); err != nil {
		return err
	}

	var inR, inW, outR, outW windows.Handle
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&inR, &inW, sa, 0); err != nil {
		return err
	}
	if err := windows.CreatePipe(&outR, &outW, sa, 0); err != nil {
		return err
	}
	for _, h := range []windows.Handle{inW, outR} {
		if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return err
		}
	}
	var console windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &console); err != nil {
		return err
	}
	_ = windows.CloseHandle(inR)
	_ = windows.CloseHandle(outW)
	output := os.NewFile(uintptr(outR), "conpty-output")
	r.in = os.NewFile(uintptr(inW), "conpty-input")

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console)); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES,
			StdInput: windows.InvalidHandle, StdOutput: windows.InvalidHandle, StdErr: windows.InvalidHandle,
		},
		ProcThreadAttributeList: attrs.List(),
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return err
	}
	var dir *uint16
	if cwd != "" {
		if dir, err = windows.UTF16PtrFromString(cwd); err != nil {
			return err
		}
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdLine, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, nil, dir, &si.StartupInfo, &pi); err != nil {
		return err
	}
	r.start = time.Now()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1<<16)
		for {
			n, err := output.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				r.event("o", chunk)
				// Answer cursor position queries as a terminal does.
				if strings.Contains(chunk, "\x1b[6n") {
					r.send("\x1b[1;1R")
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// Flush every two seconds so a running recording can be inspected.
	stopFlush := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.flush()
			case <-stopFlush:
				return
			}
		}
	}()

	// Report window focus, as a focused terminal tab does.
	time.Sleep(300 * time.Millisecond)
	r.send("\x1b[I")
	_ = os.Remove(r.inject)
	if script != "" {
		data, err := os.ReadFile(script)
		if err != nil {
			return err
		}
		r.runSteps(string(data))
	}
	// Leave a short tail, then stop the program.
	time.Sleep(500 * time.Millisecond)
	_ = windows.TerminateProcess(pi.Process, 0)
	_, _ = windows.WaitForSingleObject(pi.Process, 5000)
	windows.ClosePseudoConsole(console)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	close(stopFlush)
	r.flush()
	fmt.Println("wrote", outPath, time.Since(r.start).Round(time.Millisecond))
	return nil
}
