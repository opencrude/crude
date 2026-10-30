package lsp

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServerLaunchEnvironmentHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "emit-launch-environment-fixture" {
		return
	}
	_, unknown := os.LookupEnv("POWERNAP_UNLISTED_FIXTURE")
	data, err := json.Marshal(map[string]any{
		"unknownPresent": unknown,
		"explicit":       os.Getenv("POWERNAP_EXPLICIT_FIXTURE"),
	})
	if err != nil {
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		os.Exit(2)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestServerLaunchUsesOnlyPreparedEnvironmentAndItsExecutableSearch(t *testing.T) {
	t.Setenv("POWERNAP_UNLISTED_FIXTURE", "unknown-provider-fixture-not-a-real-secret")
	t.Setenv("PATH", t.TempDir()) // the executable is absent from ambient PATH
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured-search", true: "explicit-empty"}[empty], func(t *testing.T) {
			values := map[string]string{}
			command := program
			want := ""
			if !empty {
				values["PATH"] = filepath.Dir(program)
				values["POWERNAP_EXPLICIT_FIXTURE"] = "detached-explicit"
				command = filepath.Base(program)
				want = "detached-explicit"
			}
			environment := closeTestEnvironment(t, values)
			values["POWERNAP_EXPLICIT_FIXTURE"] = "late-mutation"
			stream, err := startServerProcess(t.Context(), ClientConfig{
				Command: command, Environment: environment,
				Args: []string{"-test.run=^TestServerLaunchEnvironmentHelper$", "--", "emit-launch-environment-fixture"},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stream.Close(); err != nil {
					t.Error(err)
				}
			})
			var got struct {
				UnknownPresent bool   `json:"unknownPresent"`
				Explicit       string `json:"explicit"`
			}
			if err := json.NewDecoder(stream).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.UnknownPresent || got.Explicit != want {
				t.Fatal("actual subprocess inherited ambient data, ignored its prepared PATH, or observed caller mutation")
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientRefusesUnpreparedEnvironmentBeforeExecutableOrTransport(t *testing.T) {
	wrapped := false
	client, err := NewClient(ClientConfig{
		Command: "must-not-be-resolved-with-unprepared-environment",
		TransportWrap: func(reader io.Reader, writer io.Writer) (io.Reader, io.Writer) {
			wrapped = true
			return reader, writer
		},
	})
	if !errors.Is(err, ErrProcessEnvironment) || client != nil || wrapped {
		t.Fatal("unprepared environment reached executable construction")
	}
	empty, err := NewProcessEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLaunchEnvironment(empty); (err != nil) != (runtime.GOOS == "windows") {
		t.Fatal("empty complete environment gained an implicit platform inheritance path")
	}
}

func TestServerLaunchErrorsNeverEchoPreparedSearchDirectories(t *testing.T) {
	sensitivePath := filepath.Join(t.TempDir(), "sensitive-fixture-not-a-real-secret")
	environment := closeTestEnvironment(t, map[string]string{"PATH": sensitivePath})
	client, err := NewClient(ClientConfig{Command: "not-installed-fixture", Environment: environment})
	if client != nil || err == nil || strings.Contains(err.Error(), sensitivePath) {
		t.Fatal("missing executable error exposed prepared search data or a process")
	}
}
