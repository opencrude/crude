// Package testterminal supplies owned native child workloads for PTY tests.
package testterminal

import (
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/term"
)

const marker = "--crude-terminal-fixture"

func Command(t testing.TB, role string, args ...string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe, append([]string{marker, role}, args...)
}

// Run must be called before testing parses its flags.
func Run() (int, bool) {
	if len(os.Args) < 2 || os.Args[1] != marker {
		return 0, false
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

func run(args []string) (result error) {
	if len(args) == 0 {
		return fmt.Errorf("missing fixture role")
	}
	switch args[0] {
	case "idle":
		if len(args) != 1 {
			return fmt.Errorf("idle arguments")
		}
		time.Sleep(300 * time.Second)
		return nil
	case "emit":
		if len(args) != 2 {
			return fmt.Errorf("emit arguments")
		}
		_, err := io.WriteString(os.Stdout, args[1])
		return err
	case "echo", "query", "emit-after-key":
		if (args[0] == "echo" && len(args) > 2) || (args[0] == "query" && len(args) != 1) ||
			(args[0] == "emit-after-key" && len(args) != 2) {
			return fmt.Errorf("invalid fixture arguments")
		}
		fd := int(os.Stdin.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer func() {
			if err := term.Restore(fd, state); err != nil && result == nil {
				result = err
			}
		}()
		restore, err := enableVirtualInput(fd)
		if err != nil {
			return err
		}
		defer func() {
			if err := restore(); err != nil && result == nil {
				result = err
			}
		}()
		if args[0] == "emit-after-key" {
			if _, err := io.WriteString(os.Stdout, "CHILD_READY"); err != nil {
				return err
			}
			var key [1]byte
			if _, err := io.ReadFull(os.Stdin, key[:]); err != nil {
				return err
			}
			if _, err := io.WriteString(os.Stdout, args[1]); err != nil {
				return err
			}
			_, err := io.Copy(io.Discard, os.Stdin)
			return err
		}
		if args[0] == "echo" {
			if len(args) == 2 {
				if _, err := io.WriteString(os.Stdout, args[1]); err != nil {
					return err
				}
			}
			_, err := io.Copy(os.Stdout, os.Stdin)
			return err
		}
		if _, err := io.WriteString(os.Stdout, "\x1b[H\x1b[6n"); err != nil {
			return err
		}
		reply := make([]byte, 0, 64)
		var b [1]byte
		for len(reply) < cap(reply) {
			if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
				return err
			}
			reply = append(reply, b[0])
			if b[0] == 'R' {
				if string(reply) != "\x1b[1;1R" {
					return fmt.Errorf("unexpected home-position DSR reply %q", reply)
				}
				_, err := fmt.Fprintf(os.Stdout, "REPLY:%q\r\nFINAL", reply)
				return err
			}
		}
		return fmt.Errorf("DSR reply exceeds bound")
	default:
		return fmt.Errorf("unknown fixture role %q", args[0])
	}
}
