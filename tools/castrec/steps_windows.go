//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// runSteps runs script lines. It returns false when a quit step ran.
func (r *recorder) runSteps(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		op, arg, _ := strings.Cut(line, ":")
		switch op {
		case "sleep":
			s, _ := strconv.ParseFloat(arg, 64)
			time.Sleep(time.Duration(s * float64(time.Second)))
		case "mark":
			r.event("m", arg)
			fmt.Printf("%.2f mark %s\n", time.Since(r.start).Seconds(), arg)
		case "key":
			r.key(arg)
			time.Sleep(60 * time.Millisecond)
		case "type":
			r.typeText(arg, 0)
		case "typeslow":
			ms, text, _ := strings.Cut(arg, ":")
			d, _ := strconv.Atoi(ms)
			r.typeText(text, time.Duration(d)*time.Millisecond)
		case "typeenv":
			r.typeText(os.Getenv(arg), 0)
		case "click", "rclick", "move", "drag", "wheel":
			r.event("i", op+":"+arg)
			r.mouse(op, arg)
		case "waitfile":
			if !r.waitFile(arg) {
				return false
			}
		case "quit":
			return false
		default:
			fmt.Fprintln(os.Stderr, "castrec: unknown step", line)
		}
	}
	return true
}

// waitFile waits until path exists. Steps written to the inject file run
// while it waits, so an observer can drive a step of unknown length.
func (r *recorder) waitFile(path string) bool {
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if b, err := os.ReadFile(r.inject); err == nil {
			_ = os.Remove(r.inject)
			fmt.Printf("%.2f inject %q\n", time.Since(r.start).Seconds(), strings.TrimSpace(string(b)))
			if !r.runSteps(string(b)) {
				return false
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}
