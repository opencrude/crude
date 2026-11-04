package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"crude/insight/catalog"
	"crude/types"
)

// BuildProjectToolReview describes missing project tools without installing
// them. Its token fingerprints the preimage; it does not grant authority.
func BuildProjectToolReview(project, rootIdentity string, plan types.AssemblyPlan) (types.ProjectToolReview, error) {
	return buildProjectToolReview(project, rootIdentity, plan, catalog.Current(), catalog.CurrentTargets())
}

func reviewHash(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // The fixed preimage types contain only JSON-safe values.
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if value != "" && (len(out) == 0 || out[len(out)-1] != value) {
			out = append(out, value)
		}
	}
	return out
}

func buildProjectToolReview(project, rootIdentity string, plan types.AssemblyPlan, c *catalog.Catalog, targets []string) (types.ProjectToolReview, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(rootIdentity) == "" {
		return types.ProjectToolReview{}, errors.New("project and root identity are required")
	}
	if plan.State != types.AssemblyReady || plan.Limited || c == nil {
		return types.ProjectToolReview{}, errors.New("a complete ready assembly plan is required")
	}
	review := types.ProjectToolReview{Project: project, RootIdentity: rootIdentity, AssemblyGeneration: plan.Generation}
	byIdentity := make(map[string]int)
	for _, language := range plan.Languages {
		if language.Status != types.ToolMissing {
			continue
		}
		item := types.ProjectToolCandidate{Package: language.Package, Command: language.Command}
		if pkg, ok := c.Package(language.Package); ok {
			download, reason := pkg.DownloadFor(language.Command, targets)
			if reason == "" {
				item.Package, item.Version, item.Source, item.URL = download.Package, download.Version, download.Source, download.URL
				item.File, item.Archive, item.Bin, item.Command = download.File, download.Archive, download.Bin, download.Command
				item.Licenses = sortedUnique(download.Licenses)
				item.Installable = true
			} else {
				item.Reason = reason + " Install it yourself, then check again."
				item.Licenses = sortedUnique(pkg.Licenses)
				if recipe, ok := pkg.PackageManagerFor(language.Command); ok {
					// The literal source and program remain in the review
					// preimage, including qualifiers and subpaths. A changed
					// recipe is not the same remembered decision.
					item.Version = recipe.Version
					item.Source, item.URL = recipe.Registry, pkg.Source.ID
					if repository := recipe.Qualifiers["repository_url"]; repository != "" {
						item.Source = repository
					}
					item.Bin = recipe.ProgramKind + ":" + recipe.Program
					item.Reason = fmt.Sprintf("%s uses %s and needs %s. Crude cannot install it yet. Install it yourself, then check again.",
						item.Package, recipe.Manager, recipe.Runtime)
				}
			}
		} else {
			item.Reason = "Crude cannot install this tool yet. Install it yourself, then check again."
		}
		// Only identical download descriptions are merged. Unsupported entries
		// retain server identity, so unknown tools are never conflated.
		key := reviewHash(item)
		if !item.Installable {
			key = reviewHash(struct {
				Item   types.ProjectToolCandidate
				Server string
			}{item, language.ServerID})
		}
		index, exists := byIdentity[key]
		if !exists {
			index = len(review.Items)
			byIdentity[key] = index
			review.Items = append(review.Items, item)
		}
		review.Items[index].Languages = append(review.Items[index].Languages, language.ID)
		review.Items[index].ServerIDs = append(review.Items[index].ServerIDs, language.ServerID)
	}
	for i := range review.Items {
		item := &review.Items[i]
		item.Languages = sortedUnique(item.Languages)
		item.ServerIDs = sortedUnique(item.ServerIDs)
		item.ID = reviewHash(*item)
	}
	sort.Slice(review.Items, func(i, j int) bool { return review.Items[i].ID < review.Items[j].ID })
	review.Token = reviewHash(review)
	return review, nil
}
