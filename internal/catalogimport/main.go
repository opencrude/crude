// Command catalogimport regenerates insight/catalog/catalog.json from the
// pinned upstream inputs in pins.json and insight/catalog/authored.json.
// It is a development tool, never part of the product. It downloads only the
// two pinned files, verifies their SHA-256, and executes nothing from them.
//
//	go run ./internal/catalogimport            # download, verify, write
//	go run ./internal/catalogimport -check     # fail when catalog.json is stale
//	go run ./internal/catalogimport -mason registry.json.zip -helix languages.toml
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"crude/insight/catalog"
	"crude/network"
)

//go:embed pins.json
var pinsData []byte

const maxInputBytes = 32 << 20

func main() {
	masonPath := flag.String("mason", "", "local registry.json.zip (default: download the pinned release)")
	helixPath := flag.String("helix", "", "local languages.toml (default: download the pinned commit)")
	authoredPath := flag.String("authored", "insight/catalog/authored.json", "authored overlay")
	outPath := flag.String("out", "insight/catalog/catalog.json", "generated catalog")
	check := flag.Bool("check", false, "compare with the existing output instead of writing it")
	verbose := flag.Bool("v", false, "list every skipped upstream entry")
	flag.Parse()
	if err := run(*masonPath, *helixPath, *authoredPath, *outPath, *check, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "catalogimport:", err)
		os.Exit(1)
	}
}

func run(masonPath, helixPath, authoredPath, outPath string, check, verbose bool) error {
	var pins Pins
	if err := json.Unmarshal(pinsData, &pins); err != nil {
		return fmt.Errorf("pins.json: %w", err)
	}
	masonZip, err := input(masonPath, pins.Mason.URL, pins.Mason.SHA256)
	if err != nil {
		return fmt.Errorf("mason registry: %w", err)
	}
	masonJSON, err := registryJSON(masonZip)
	if err != nil {
		return fmt.Errorf("mason registry: %w", err)
	}
	helixTOML, err := input(helixPath, pins.Helix.URL, pins.Helix.SHA256)
	if err != nil {
		return fmt.Errorf("helix languages: %w", err)
	}
	authored, err := os.ReadFile(authoredPath)
	if err != nil {
		return err
	}
	doc, report, err := Import(pins, helixTOML, masonJSON, authored)
	if err != nil {
		return err
	}
	encoded, err := catalog.Encode(doc)
	if err != nil {
		return err
	}
	fmt.Printf("catalog: %d languages, %d servers, %d packages, %d bytes; %d upstream entries skipped\n",
		report.Languages, report.Servers, report.Packages, len(encoded), len(report.Skipped))
	if verbose {
		for _, line := range report.Skipped {
			fmt.Println("  skipped:", line)
		}
	}
	if check {
		existing, err := os.ReadFile(outPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, encoded) {
			return fmt.Errorf("%s is stale; run go run ./internal/catalogimport", outPath)
		}
		return nil
	}
	return os.WriteFile(outPath, encoded, 0o644)
}

func input(path, url, wantSHA256 string) ([]byte, error) {
	var data []byte
	var err error
	if path != "" {
		data, err = os.ReadFile(path)
	} else {
		data, err = download(url)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, fmt.Errorf("sha256 is %s, pins.json expects %s", got, wantSHA256)
	}
	return data, nil
}

func download(url string) ([]byte, error) {
	// The shared client applies Crude's proxy and TLS policy.
	client := network.NewClient(60 * time.Second)
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", url, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("input is larger than the import limit")
	}
	return data, nil
}

func registryJSON(archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if file.Name != "registry.json" {
			continue
		}
		if file.UncompressedSize64 > maxInputBytes {
			return nil, errors.New("registry.json is larger than the import limit")
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(io.LimitReader(rc, maxInputBytes))
	}
	return nil, errors.New("archive has no registry.json")
}
