package workspace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"

	"crude/insight/catalog"
	"crude/toolstore"
	"crude/types"
)

type projectToolInstaller interface {
	Install(context.Context, toolstore.Request) (toolstore.Tool, error)
}

// The owner binds these checks to an exact consumed native decision and the
// current project lifetime. Neither the review hash nor the audit record is
// authority. AdmitPublication must linearize against revocation and check ctx
// while holding that owner's short in-memory gate.
type projectToolEffectAuthority interface {
	Validate(context.Context, string) error
	AdmitPublication(context.Context, string) error
}

type projectToolInstallLimits struct {
	MaxDownloadBytes, MaxExtractedBytes int64
	MaxEntries                          int
}

// A result is returned for every selected item, including items not attempted.
// Err retains the underlying cause for audit; Message is suitable for display.
type projectToolInstallResult struct {
	ItemID, Message string
	Tool            toolstore.Tool
	Attempted       bool
	Err             error
}

type projectToolAuthorityError struct{ cause error }

func (e *projectToolAuthorityError) Error() string { return e.cause.Error() }
func (e *projectToolAuthorityError) Unwrap() error { return e.cause }

// installReviewedProjectTools performs blocking I/O. It must run off the UI
// thread under a cancellable lifecycle owner. This is not an approval authority:
// before calling, the authority must verify and durably record the exact human
// decision bound to this review token, project and selection, and fence replay.
// This worker does not mark usage or start a server. Those later effects need
// their own current admission.
func installReviewedProjectTools(ctx context.Context, installer projectToolInstaller, authority projectToolEffectAuthority, review types.ProjectToolReview, approvedIDs []string, approvalID, projectID string, limits projectToolInstallLimits) ([]projectToolInstallResult, error) {
	return installReviewedProjectToolsWithCatalog(ctx, installer, authority, review, approvedIDs, approvalID, projectID, limits, catalog.Current(), catalog.CurrentTargets())
}

func installReviewedProjectToolsWithCatalog(ctx context.Context, installer projectToolInstaller, authority projectToolEffectAuthority, review types.ProjectToolReview, approvedIDs []string, approvalID, projectID string, limits projectToolInstallLimits, c *catalog.Catalog, targets []string) ([]projectToolInstallResult, error) {
	if installer == nil || authority == nil || c == nil || strings.TrimSpace(approvalID) == "" || strings.TrimSpace(projectID) == "" || projectID != review.Project || strings.TrimSpace(review.RootIdentity) == "" {
		return nil, errors.New("a matching project and approval are required")
	}
	if limits.MaxDownloadBytes <= 0 || limits.MaxExtractedBytes <= 0 || limits.MaxEntries < 0 {
		return nil, errors.New("valid download and extraction budgets are required")
	}
	copyReview := types.CloneProjectToolReview(review)
	token := copyReview.Token
	copyReview.Token = ""
	if token == "" || reviewHash(copyReview) != token {
		return nil, errors.New("the tool review changed; review it again")
	}
	items := make(map[string]types.ProjectToolCandidate, len(review.Items))
	for _, item := range review.Items {
		id := item.ID
		item.ID = ""
		if id == "" || reviewHash(item) != id {
			return nil, errors.New("a reviewed tool changed; review it again")
		}
		if _, exists := items[id]; exists {
			return nil, errors.New("the tool review contains duplicate items")
		}
		item.ID = id
		items[id] = item
	}
	if len(approvedIDs) == 0 {
		return nil, errors.New("select at least one tool")
	}
	selected := make([]types.ProjectToolCandidate, 0, len(approvedIDs))
	seen := make(map[string]bool, len(approvedIDs))
	for _, id := range approvedIDs {
		item, ok := items[id]
		if !ok || seen[id] || !item.Installable {
			return nil, errors.New("the selected tools do not match the review")
		}
		seen[id] = true
		pkg, ok := c.Package(item.Package)
		if !ok {
			return nil, errors.New("the tool catalog changed; review the tools again")
		}
		d, reason := pkg.DownloadFor(item.Command, targets)
		licenses := sortedUnique(append([]string(nil), d.Licenses...))
		if reason != "" || d.Package != item.Package || d.Version != item.Version || d.Source != item.Source ||
			d.URL != item.URL || d.File != item.File || d.Archive != item.Archive || d.Bin != item.Bin ||
			d.Command != item.Command || !reflect.DeepEqual(licenses, item.Licenses) ||
			item.ChecksumPublished || item.SizeKnown || item.SizeBytes != 0 || item.Reason != "" {
			return nil, errors.New("the tool catalog changed; review the tools again")
		}
		selected = append(selected, item)
	}
	results := make([]projectToolInstallResult, len(selected))
	for i, item := range selected {
		results[i] = projectToolInstallResult{ItemID: item.ID, Message: "Crude did not try to install this tool."}
	}
	var failures error
	for i, item := range selected {
		if err := ctx.Err(); err != nil {
			return results, errors.Join(failures, err)
		}
		if err := authority.Validate(ctx, item.ID); err != nil {
			results[i].Err = err
			return results, errors.Join(failures, err)
		}
		if err := ctx.Err(); err != nil {
			results[i].Err = err
			return results, errors.Join(failures, err)
		}
		results[i].Attempted = true
		request := toolstore.Request{
			Package: item.Package, Version: item.Version, Source: item.Source, URL: item.URL,
			Archive: item.Archive, Bin: item.Bin, Command: item.Command,
			Licenses: append([]string(nil), item.Licenses...), InstalledBy: "user", Approval: approvalID,
			MaxDownloadBytes: limits.MaxDownloadBytes, MaxExtractedBytes: limits.MaxExtractedBytes, MaxEntries: limits.MaxEntries,
			PreparePublication: func(ctx context.Context) error {
				if err := authority.Validate(ctx, item.ID); err != nil {
					return &projectToolAuthorityError{err}
				}
				return nil
			},
			AdmitPublication: func(ctx context.Context) error {
				if err := authority.AdmitPublication(ctx, item.ID); err != nil {
					return &projectToolAuthorityError{err}
				}
				return nil
			},
		}
		tool, err := installer.Install(ctx, request)
		results[i].Tool, results[i].Err = tool, err
		if err != nil {
			results[i].Message = fmt.Sprintf("Crude could not install %s. Check the details, then try again.", item.Package)
			var unconfirmed *toolstore.PublicationUnconfirmedError
			if errors.As(err, &unconfirmed) {
				results[i].Message = fmt.Sprintf("Crude cannot confirm that %s was installed. Check the tools before you try again.", item.Package)
				return results, errors.Join(failures, err)
			}
			var unverified *toolstore.EvaluationUnverifiedError
			if errors.As(err, &unverified) {
				results[i].Message = "Crude could not stop the tool check. Check the tools before you try again."
				return results, errors.Join(failures, err)
			}
			var authorityError *projectToolAuthorityError
			if errors.As(err, &authorityError) {
				results[i].Message = fmt.Sprintf("Crude cannot finish setting up %s. Review the tools again.", item.Package)
				return results, errors.Join(failures, err)
			}
			var networkError net.Error
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && errors.As(err, &networkError) {
				results[i].Message = fmt.Sprintf("Crude could not download %s. Check the network connection, then try again.", item.Package)
			}
			failures = errors.Join(failures, err)
			if ctx.Err() != nil {
				return results, failures
			}
			continue
		}
		if err = ctx.Err(); err == nil {
			err = authority.Validate(ctx, item.ID)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			results[i].Err = err
			results[i].Message = fmt.Sprintf("Crude cannot finish setting up %s. Review the tools again.", item.Package)
			return results, errors.Join(failures, err)
		}
		results[i].Message = fmt.Sprintf("Crude installed %s.", item.Package)
	}
	return results, failures
}
