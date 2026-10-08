package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"crude/prompt"
	"crude/session"
)

const (
	commitMessageDiffLimit = 48 << 10 // bytes of diff sent to the model
	commitMessageFileLimit = 12 << 10 // bytes of one file's diff
)

// CommitMessageWriter drafts a commit message with the Aeron model. It is
// prepared on the UI goroutine (the configuration is read there); Write only
// performs repository reads and the model request.
type CommitMessageWriter struct {
	Model     string
	completer session.TextCompleter
	git       *GitService
}

// PrepareCommitMessageWriter resolves the Aeron model now and fails when no
// usable model is configured, so the UI can say why before starting.
func (w *Workspace) PrepareCommitMessageWriter() (CommitMessageWriter, error) {
	if w == nil || w.sessionCfg == nil {
		return CommitMessageWriter{}, errors.New("no model is configured for this workspace")
	}
	completer, err := w.sessionCfg.AeronCompleter()
	if err != nil {
		return CommitMessageWriter{}, err
	}
	service := w.Git
	if service == nil {
		service = NewGitService(w.RootPath)
	}
	return CommitMessageWriter{Model: completer.Model, completer: completer, git: service}, nil
}

// Write describes exactly what the commit would record: the staged set, or
// every listed change when all will be staged. emit streams the draft.
func (writer CommitMessageWriter) Write(ctx context.Context, stageAll bool, emit func(string)) (string, error) {
	if writer.git == nil {
		return "", errors.New("git service is unavailable")
	}
	snapshot, err := writer.git.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	var comparisons []GitComparison
	if stageAll {
		results, err := writer.git.CompareChanges(ctx, snapshot.Changes)
		if err != nil {
			return "", err
		}
		for _, result := range results {
			if result.Err == nil {
				comparisons = append(comparisons, result.Comparison)
			}
		}
	} else {
		review, err := writer.git.ReviewCommit(ctx)
		if err != nil {
			return "", err
		}
		comparisons = review.Comparisons
	}
	if len(comparisons) == 0 {
		return "", errors.New("there are no changes to describe")
	}
	subjects := make([]string, 0, len(snapshot.RecentCommits))
	for _, commit := range snapshot.RecentCommits {
		subjects = append(subjects, commit.Subject)
	}
	request := prompt.CommitMessageRequest(snapshot.Branch, subjects, commitMessageDiff(comparisons))
	text, err := writer.completer.Complete(ctx, prompt.CommitMessageSystem(), request, emit)
	if err != nil {
		return "", err
	}
	message := CleanCommitMessage(text)
	if message == "" {
		return "", errors.New("the model returned an empty commit message")
	}
	return message, nil
}

// commitMessageDiff renders bounded unified diffs; binary and oversized files
// are named rather than dumped.
func commitMessageDiff(comparisons []GitComparison) string {
	var out strings.Builder
	for _, comparison := range comparisons {
		if out.Len() >= commitMessageDiffLimit {
			fmt.Fprintf(&out, "… %d more files not shown\n", len(comparisons))
			break
		}
		path := comparison.Path
		switch {
		case comparison.Binary:
			fmt.Fprintf(&out, "Binary file %s changed\n", path)
			continue
		case !comparison.BeforePresent:
			fmt.Fprintf(&out, "New file %s\n", path)
		case !comparison.AfterPresent:
			fmt.Fprintf(&out, "Deleted file %s\n", path)
		}
		diff := udiff.Unified("a/"+path, "b/"+path, comparison.Before, comparison.After)
		if len(diff) > commitMessageFileLimit {
			diff = diff[:commitMessageFileLimit] + "\n… diff truncated\n"
		}
		out.WriteString(diff)
	}
	return out.String()
}

// CleanCommitMessage strips fences and quoting a model may add around the
// message and normalizes surrounding whitespace.
func CleanCommitMessage(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		if newline := strings.IndexByte(text, '\n'); newline >= 0 && !strings.Contains(text[:newline], " ") {
			text = text[newline+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	return strings.Trim(strings.TrimSpace(text), "\"`")
}
