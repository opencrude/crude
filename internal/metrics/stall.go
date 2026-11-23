package metrics

import (
	"bufio"
	"bytes"
	"path"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"time"
)

// UI stall evidence. When one UI loop iteration is still running after
// uiStallCapture, a watchdog records the UI goroutine's stack while it is
// stalled. A freeze then names the code that caused it, not only the message
// type.
//
// Cost: BeginUIWork and EndUIWork reset and stop one reused timer. Only a
// stall pays for a capture. The goroutine profile collects stacks while the
// program runs, with two brief stop-the-world pauses; runtime.Stack with all
// goroutines would stop the world for the whole dump. Captures are rate
// limited.
const (
	uiStallCapture  = 50 * time.Millisecond
	uiStallInterval = time.Second
	maxStallFrames  = 32
	maxFrameText    = 200
)

var uiWork struct {
	mu       sync.Mutex
	timer    *time.Timer
	seq      uint64 // current iteration
	busy     bool
	stack    []string // captured during iteration stackSeq
	stackSeq uint64
	last     time.Time
	// Functions on the UI goroutine's stack when it was bound. They identify
	// it among all goroutines; a stalled UI stack shares its loop frames.
	signature map[string]struct{}
}

// BeginUIWork marks the start of one UI loop iteration. Call it on the UI
// goroutine, then EndUIWork when the iteration has handed off its frame.
func BeginUIWork() {
	uiWork.mu.Lock()
	uiWork.seq++
	uiWork.busy, uiWork.stack = true, nil
	if uiWork.signature != nil {
		if uiWork.timer == nil {
			uiWork.timer = time.AfterFunc(uiStallCapture, captureUIStall)
		} else {
			uiWork.timer.Reset(uiStallCapture)
		}
	}
	uiWork.mu.Unlock()
}

// EndUIWork ends the iteration. It returns the UI goroutine's stack captured
// while the iteration was stalled, or nil.
func EndUIWork() []string {
	uiWork.mu.Lock()
	defer uiWork.mu.Unlock()
	uiWork.busy = false
	if uiWork.timer != nil {
		uiWork.timer.Stop()
	}
	if uiWork.stackSeq != uiWork.seq {
		return nil
	}
	stack := uiWork.stack
	uiWork.stack = nil
	return stack
}

// bindUISignature runs once, on the UI goroutine, when it is bound.
func bindUISignature() {
	var pcs [64]uintptr
	n := runtime.Callers(1, pcs[:])
	signature := make(map[string]struct{}, n)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			signature[frame.Function] = struct{}{}
		}
		if !more {
			break
		}
	}
	uiWork.mu.Lock()
	uiWork.signature = signature
	uiWork.mu.Unlock()
}

func captureUIStall() {
	uiWork.mu.Lock()
	at := now()
	if !uiWork.busy || at.Sub(uiWork.last) < uiStallInterval {
		uiWork.mu.Unlock()
		return
	}
	seq, signature := uiWork.seq, uiWork.signature
	uiWork.last = at
	uiWork.mu.Unlock()

	var profile bytes.Buffer
	// debug=1 uses the concurrent goroutine profile; debug=2 stops the world.
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 1); err != nil {
		return
	}
	stack := uiStack(profile.Bytes(), signature)

	uiWork.mu.Lock()
	if uiWork.busy && uiWork.seq == seq && stack != nil {
		uiWork.stack, uiWork.stackSeq = stack, seq
	}
	uiWork.mu.Unlock()
}

// uiStack returns the frames of the goroutine-profile record that shares the
// most functions with signature, outermost frames last. The profile hides
// runtime frames, so only the UI goroutine's own loop frames can match; an
// ambiguous best match returns nil.
func uiStack(profile []byte, signature map[string]struct{}) []string {
	var best []string
	bestScore, tied := 0, false
	var record []string
	score := 0
	flush := func() {
		switch {
		case score > bestScore:
			best, bestScore, tied = record, score, false
		case score == bestScore && score > 0:
			tied = true
		}
		record, score = nil, 0
	}
	lines := bufio.NewScanner(bytes.NewReader(profile))
	lines.Buffer(make([]byte, 0, 4096), 1<<20)
	for lines.Scan() {
		line := lines.Text()
		if line == "" {
			flush()
			continue
		}
		// "#\t0x4a5b6c\tpkg.Func+0x12\t\tC:/path/file.go:123"; a tab writer
		// aligns the columns, so empty fields separate them.
		fields := slices.DeleteFunc(strings.Split(line, "\t"), func(field string) bool { return field == "" })
		if len(fields) != 4 || fields[0] != "#" {
			continue
		}
		function, _, _ := strings.Cut(fields[2], "+0x")
		if _, ok := signature[function]; ok {
			score++
		}
		if len(record) < maxStallFrames {
			file, lineNumber, _ := strings.Cut(path.Base(strings.ReplaceAll(fields[3], `\`, "/")), ":")
			frame := function + " (" + file + ":" + lineNumber + ")"
			record = append(record, frame[:min(len(frame), maxFrameText)])
		}
	}
	flush()
	if tied {
		return nil
	}
	return best
}
