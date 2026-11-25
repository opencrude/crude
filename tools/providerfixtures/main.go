package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("choose replay, serve or capture")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	name := flags.String("wire", "", "wire name")
	dir := flags.String("dir", "", "cassette directory (must be new for capture)")
	model := flags.String("model", "", "model ID; required for capture")
	live := flags.Bool("live", false, "send one fixed synthetic request; charges may apply")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dir == "" {
		return errors.New("supply -wire and -dir only, plus -live and -model for capture")
	}
	s, err := lookup(*name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch args[0] {
	case "capture":
		if !*live || !modelID.MatchString(*model) {
			return errors.New("capture requires -live and -model; charges may apply")
		}
		var key string
		if s.name != "ollama" {
			b, err := io.ReadAll(io.LimitReader(in, 16385))
			if err != nil || len(b) > 16384 {
				return errors.New("invalid credential on stdin")
			}
			key = strings.TrimSpace(string(b))
			if key == "" || strings.ContainsAny(key, " \r\n\t") {
				return errors.New("supply one token on stdin")
			}
		}
		t := http.DefaultTransport.(*http.Transport).Clone()
		defer t.CloseIdleConnections()
		return record(ctx, *dir, s, s.base, *model, key, t)
	case "replay", "serve":
		if *live || *model != "" {
			return errors.New("offline mode accepts no model or live option")
		}
		frames, err := replay(ctx, *dir, s)
		if err != nil {
			return err
		}
		if args[0] == "replay" {
			_, err = io.WriteString(out, marshalFrames(s, frames))
			return err
		}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer l.Close()
		fmt.Fprintf(out, "Replay base: http://%s%s\n", l.Addr(), s.basePath())
		server := http.Server{Handler: handler(s, frames), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
		return server.Serve(l)
	}
	return errors.New("choose replay, serve or capture")
}
func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
