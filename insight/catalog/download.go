package catalog

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Download is one installable release file of a package for this computer.
// Every field is validated; a package that cannot be described this way is
// not installable by Crude and the reason says why in plain words.
type Download struct {
	// Package and Version identify the tool.
	Package, Version string
	// URL is the HTTPS address of the release file.
	URL string
	// File is the release file name.
	File string
	// Archive is one of "zip", "tar.gz", "tar.xz", "gz" or "raw".
	Archive string
	// Bin is the slash-separated program path inside the unpacked tree. For
	// "gz" and "raw" it is the single file name the store writes.
	Bin string
	// Command is the program name the server launches.
	Command string
	// Source names where the file comes from, for example
	// "github.com/owner/repo".
	Source   string
	Licenses []string
}

var githubName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9_.][A-Za-z0-9_.-]{0,99}$`)

// archiveFor names the archive kind of a release file, or "" when Crude
// cannot unpack it.
func archiveFor(file string) string {
	lower := strings.ToLower(file)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".vsix"):
		return "zip"
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".7z"),
		strings.HasSuffix(lower, ".dmg"), strings.HasSuffix(lower, ".msi"), strings.HasSuffix(lower, ".pkg"),
		strings.HasSuffix(lower, ".deb"), strings.HasSuffix(lower, ".rpm"):
		return ""
	case strings.HasSuffix(lower, ".gz"):
		return "gz"
	}
	return "raw"
}

// DownloadFor resolves the release file that serves command on one of the
// targets, tried in preference order. It supports release files published
// on GitHub; other sources (language package managers, source builds) are
// not installed by Crude yet. The second result is the plain reason when the
// package is not installable here.
func (p Package) DownloadFor(command string, targets []string) (Download, string) {
	if p.Source.Type() != "github" {
		return Download{}, fmt.Sprintf("%s is installed with a language package manager. Crude cannot install it yet.", p.Name)
	}
	repo, version := p.Source.Name(), p.Source.Version()
	if !githubName.MatchString(repo) || version == "" || strings.ContainsAny(version, "/\\?#") || strings.Contains(version, "..") {
		return Download{}, fmt.Sprintf("The catalog entry of %s has no exact release.", p.Name)
	}
	asset, ok := p.AssetFor(targets)
	if !ok {
		return Download{}, fmt.Sprintf("No download of %s is available for this computer.", p.Name)
	}
	file := asset.File
	if file == "" || strings.ContainsAny(file, "/\\:?#") || file == "." || file == ".." {
		// A "file:member/" form names a folder inside an archive, which the
		// store does not unpack selectively.
		return Download{}, fmt.Sprintf("The release file of %s has a form that Crude cannot unpack.", p.Name)
	}
	archive := archiveFor(file)
	if archive == "" {
		return Download{}, fmt.Sprintf("The release file of %s has a form that Crude cannot unpack.", p.Name)
	}
	if command == "" {
		return Download{}, fmt.Sprintf("The catalog entry of %s names no program.", p.Name)
	}
	binTemplate, ok := p.Bin[command]
	if !ok {
		return Download{}, fmt.Sprintf("%s does not provide the program %s.", p.Name, command)
	}
	var bin string
	switch binTemplate {
	case "{{source.asset.bin}}":
		bin = asset.Bin
	case "{{source.asset.file}}":
		bin = asset.File
	default:
		if strings.Contains(binTemplate, "{{") {
			return Download{}, fmt.Sprintf("The catalog entry of %s starts its program in a way that Crude does not support yet.", p.Name)
		}
		bin = binTemplate
	}
	if archive == "gz" && bin == "" {
		bin = strings.TrimSuffix(file, path.Ext(file))
	}
	if archive == "raw" && bin == "" {
		bin = file
	}
	clean := path.Clean(bin)
	if bin == "" || clean != bin || path.IsAbs(bin) || bin == "." || strings.HasPrefix(bin, "../") || strings.Contains(bin, "\\") ||
		(archive == "gz" || archive == "raw") && strings.Contains(bin, "/") {
		return Download{}, fmt.Sprintf("The catalog entry of %s names no safe program path.", p.Name)
	}
	address := (&url.URL{Scheme: "https", Host: "github.com",
		Path: "/" + repo + "/releases/download/" + version + "/" + file}).String()
	return Download{Package: p.Name, Version: version, URL: address, File: file, Archive: archive, Bin: bin,
		Command: command, Source: "github.com/" + repo, Licenses: append([]string(nil), p.Licenses...)}, ""
}
