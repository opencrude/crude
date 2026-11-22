package session

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"crude/agent/prompt"
	"crude/agent/sessiondb"
	"crude/agent/worktree"
	"crude/types/command"
	"crude/types/conversation"
	"crude/workspace/fileio"
)

const (
	investigationJournalKey = "plan_evidence_journal"
	investigationBlobPrefix = "plan_evidence:"
	maxEvidenceCommands     = 24
	maxEvidenceArgsBytes    = 24 << 10
	maxEvidenceOutputBytes  = 12 << 10
	maxEvidenceExcerptBytes = 24 << 10
	maxEvidenceArchiveRaw   = 8 << 20
	maxEvidenceArchiveBytes = 256 << 10
	maxEvidenceRecordBytes  = 1 << 20

	errPlanEvidenceUnavailable = PlanReviewError("This investigation record is not available. Open the plan again.")
	errPlanEvidenceChanged     = PlanReviewError("Could not verify the investigation record. The file may have changed.")
)

// The journal is the session's bounded queue of evidence for its current
// planning cycle. The before image gets a content address before process start.
// Settlement publishes a new blob rather than changing any previously read
// bytes. Proposals bind their own immutable list of addresses.
type investigationEvidenceJournal struct {
	SessionID string                       `json:"session_id"`
	Cycle     string                       `json:"cycle"`
	Entries   []investigationEvidenceEntry `json:"entries"`
	Omitted   int                          `json:"omitted,omitempty"`
}

type investigationEvidenceEntry struct {
	CheckoutID string                       `json:"checkout_id"`
	Generation uint64                       `json:"generation"`
	Ref        conversation.PlanEvidenceRef `json:"ref"`
}

func (s *Session) loadInvestigationEvidenceJournal(ctx context.Context) (investigationEvidenceJournal, error) {
	var journal investigationEvidenceJournal
	raw, err := sessiondb.LoadRuntimeState(ctx, s.db, investigationJournalKey)
	if err != nil || len(raw) == 0 {
		return journal, err
	}
	err = strictControlJSON(raw, &journal)
	if err == nil && len(journal.Entries) > maxEvidenceCommands {
		err = errors.New("invalid investigation evidence journal length")
	}
	return journal, err
}

// beginInvestigationEvidence is called by the command owner with the actual
// process arguments and directory, while it holds the investigation tool
// lease. The owner invokes the returned function with the process facts, even
// when launch fails or the run stops. A missing completion cannot read as pass.
func (s *Session) beginInvestigationEvidence(ctx context.Context, generation uint64, checkout worktree.Context, cwd string, argv []string, sourceCommand ...string) (func(command.Result) error, error) {
	cycle := planInvestigationCycleFromContext(ctx)
	if generation == 0 || checkout.ID == "" || checkout.Investigation == nil ||
		checkout.Investigation.SessionID != s.sessionID || cycle == "" || checkout.Investigation.Cycle != cycle || s.db == nil {
		return nil, errPlanEvidenceUnavailable
	}
	// Quote exact byte sequences instead of letting a script insert Markdown
	// or terminal controls in the report. Bound these before launching.
	if len(argv) == 0 || len(argv) > maxEvidenceArgsBytes || len(cwd) > maxEvidenceArgsBytes {
		return nil, errors.New("investigation command arguments exceed the evidence limit")
	}
	args := make([]string, 0, len(argv))
	total := len(strconv.Quote(cwd))
	var source string
	if len(sourceCommand) != 0 {
		if len(sourceCommand[0]) > maxEvidenceArgsBytes {
			return nil, errors.New("investigation command text exceeds the evidence limit")
		}
		source = strconv.Quote(sourceCommand[0])
		total += len(source)
	}
	for _, arg := range argv {
		if len(arg) > maxEvidenceArgsBytes {
			return nil, errors.New("investigation command arguments exceed the evidence limit")
		}
		quoted := strconv.Quote(arg)
		total += len(quoted) + 1
		if total > maxEvidenceArgsBytes {
			return nil, errors.New("investigation command arguments exceed the evidence limit")
		}
		args = append(args, quoted)
	}
	service, err := s.executionService()
	if err != nil {
		return nil, err
	}
	before, err := service.Review(ctx, checkout.ID)
	if err != nil {
		return nil, fmt.Errorf("could not verify investigation files before launch: %w", err)
	}
	if before.ContextID != checkout.ID || !validEvidenceDigest(before.BaselineDigest) || before.BaselineDigest != checkout.BaselineDigest || !validEvidenceDigest(before.Digest) {
		return nil, errors.New("investigation baseline identity could not be verified")
	}
	delta := investigationEvidenceDelta(before)
	record := investigationEvidenceRecord{
		ID: newMessageID(), Generation: generation,
		Baseline: before.BaselineDigest, BaseCommit: checkout.BaseCommit,
		CWD: strconv.Quote(cwd), Args: strings.Join(args, " "), Source: source, Before: delta,
		Started: time.Now().UTC(), Status: conversation.PlanEvidenceInconclusive,
		Note: "No terminal process result was recorded.",
	}
	label := evidenceCommandLabel(strings.Join(argv, " "))
	if source != "" {
		label = evidenceCommandLabel(sourceCommand[0])
	}
	s.planReviewMu.Lock()
	pending, err := s.stageInvestigationEvidence(ctx, checkout, record, label)
	s.planReviewMu.Unlock()
	if err != nil {
		return nil, err
	}
	var once sync.Once
	var finishErr error
	return func(result command.Result) error {
		once.Do(func() {
			finishErr = s.finishInvestigationEvidence(service, checkout.ID, pending, record, label, result)
		})
		return finishErr
	}, nil
}

// The lifetime owner may retain an active turn's exact binding after the user
// changes Plan mode for future turns. Locate the exact checkout, not the
// settings projection for the next turn.
func (s *Session) stageInvestigationEvidence(ctx context.Context, checkout worktree.Context, record investigationEvidenceRecord, label string) (conversation.PlanEvidenceRef, error) {
	bindings, err := readPlanInvestigations(ctx, s.db)
	if err != nil {
		return conversation.PlanEvidenceRef{}, err
	}
	i := slices.IndexFunc(bindings, func(b planInvestigationBinding) bool {
		return b.ID == checkout.ID && b.SourceID == checkout.SourceContextID && b.Cycle == checkout.Investigation.Cycle
	})
	if i < 0 {
		return conversation.PlanEvidenceRef{}, errPlanEvidenceUnavailable
	}
	cycle := bindings[i].Cycle
	journal, err := s.loadInvestigationEvidenceJournal(ctx)
	if err != nil {
		return conversation.PlanEvidenceRef{}, err
	}
	var retired []string
	if journal.SessionID != s.sessionID || journal.Cycle != cycle {
		for _, entry := range journal.Entries {
			retired = append(retired, entry.Ref.Digest)
		}
		journal = investigationEvidenceJournal{SessionID: s.sessionID, Cycle: cycle}
	}
	if len(journal.Entries) == maxEvidenceCommands {
		retired = append(retired, journal.Entries[0].Ref.Digest)
		journal.Entries = journal.Entries[1:]
		journal.Omitted++
	}
	ref, blob := record.blob(label)
	journal.Entries = append(journal.Entries, investigationEvidenceEntry{CheckoutID: checkout.ID, Generation: record.Generation, Ref: ref})
	return ref, s.saveInvestigationEvidence(ctx, journal, ref, blob, retired)
}

func (s *Session) finishInvestigationEvidence(service ExecutionService, checkoutID string, pending conversation.PlanEvidenceRef, record investigationEvidenceRecord, label string, result command.Result) error {
	now := time.Now().UTC()
	record.Finished = &now
	record.Status, record.Note = investigationEvidenceOutcome(result)
	record.Result = &result
	if result.OutputDescriptor.Status != command.Running && !result.Uncertain {
		// The tool context may have been cancelled; query the captured checkout
		// independently, but never wait without a deadline for postflight work.
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		after, err := service.Review(ctx, checkoutID)
		stop()
		if err != nil || after.ContextID != checkoutID || after.BaselineDigest != record.Baseline || !validEvidenceDigest(after.Digest) {
			record.After.note = "Could not verify files after the command."
			if evidenceExitDetermined(record.Status) {
				record.Status = conversation.PlanEvidenceInconclusive
				record.Note = record.After.note
			}
		} else if after.Digest == record.Before.digest {
			record.After.digest = after.Digest
			record.After.note = "The measured files match the before snapshot."
			if !record.Before.exact && evidenceExitDetermined(record.Status) {
				record.Status = conversation.PlanEvidenceInconclusive
				record.Note = "The file changes exceed the evidence limit. The exact inputs were not saved."
			}
		} else {
			record.After = investigationEvidenceDelta(after)
			if evidenceExitDetermined(record.Status) {
				record.Status = conversation.PlanEvidenceInconclusive
				record.Note = "The measured files changed during the command."
			}
		}
	}
	s.planReviewMu.Lock()
	defer s.planReviewMu.Unlock()
	ctx := context.Background()
	journal, err := s.loadInvestigationEvidenceJournal(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(journal.Entries, func(c investigationEvidenceEntry) bool {
		return c.CheckoutID == checkoutID && c.Ref.Digest == pending.Digest
	})
	if journal.SessionID != s.sessionID || i < 0 {
		return errPlanEvidenceUnavailable
	}
	ref, blob := record.blob(label)
	if record.Source == "" && result.Command != "" {
		ref.Command = evidenceCommandLabel(result.Command)
	}
	journal.Entries[i].Ref = ref
	if err := s.saveInvestigationEvidence(ctx, journal, ref, blob, []string{pending.Digest}); err != nil {
		return err
	}
	if (result.OutputDescriptor.Status == command.Completed || result.OutputDescriptor.Status == command.Failed) && record.Status == conversation.PlanEvidenceInconclusive {
		return PlanReviewError("This command did not establish a result for the measured files. " + record.Note)
	}
	return nil
}

func evidenceExitDetermined(status conversation.PlanEvidenceStatus) bool {
	return status == conversation.PlanEvidencePassed || status == conversation.PlanEvidenceFailed
}

func investigationEvidenceOutcome(r command.Result) (conversation.PlanEvidenceStatus, string) {
	switch {
	case r.Cancelled || r.TimedOut || r.OutputDescriptor.Status == command.Killed:
		return conversation.PlanEvidenceInterrupted, "The command was stopped or timed out."
	case r.Uncertain || r.ExitCode == nil || *r.ExitCode < 0:
		return conversation.PlanEvidenceInconclusive, "No confirmed process exit code was recorded."
	case r.OutputDescriptor.Status == command.Failed && *r.ExitCode != 0:
		return conversation.PlanEvidenceFailed, "The command exited with a nonzero code."
	case r.OutputDescriptor.Status == command.Completed && *r.ExitCode == 0 && r.Error == "":
		return conversation.PlanEvidencePassed, "The command exited with code 0."
	default:
		return conversation.PlanEvidenceInconclusive, "The process result did not establish a pass or failure."
	}
}

func (s *Session) saveInvestigationEvidence(ctx context.Context, journal investigationEvidenceJournal, ref conversation.PlanEvidenceRef, blob []byte, retire []string) error {
	if len(blob) > maxEvidenceRecordBytes {
		return errors.New("investigation record exceeds the evidence limit")
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	updates := []sessiondb.RuntimeStateUpdate{
		{Key: investigationJournalKey, Value: raw},
		{Key: investigationBlobPrefix + ref.Digest, Value: blob},
	}
	return sessiondb.CommitRuntimeState(ctx, s.db, updates, evidenceRetiredKeys(s.planReview.Load(), journal, retire)...)
}

func evidenceRetiredKeys(review *planReviewRecord, journal investigationEvidenceJournal, candidates []string) []string {
	if len(candidates) == 0 {
		return nil
	}
	keep := make(map[string]bool)
	if review != nil {
		for _, revision := range review.Revisions {
			for _, ref := range revision.Evidence {
				keep[ref.Digest] = true
			}
		}
	}
	for _, entry := range journal.Entries {
		keep[entry.Ref.Digest] = true
	}
	var keys []string
	for _, digest := range candidates {
		if !keep[digest] && validEvidenceDigest(digest) {
			keys = append(keys, investigationBlobPrefix+digest)
			keep[digest] = true
		}
	}
	return keys
}

// proposedPlanEvidence binds only the planning cycle and input source of this
// completed turn. A fork keeps its saved revisions, not the source's journal.
func (s *Session) proposedPlanEvidence(ctx context.Context, journal investigationEvidenceJournal, generation uint64, cycle, sourceID string) ([]conversation.PlanEvidenceRef, int, error) {
	if cycle == "" || journal.Cycle != cycle || journal.SessionID != s.sessionID {
		return nil, 0, nil
	}
	bindings, err := readPlanInvestigations(ctx, s.db)
	if err != nil {
		return nil, 0, err
	}
	i := slices.IndexFunc(bindings, func(b planInvestigationBinding) bool { return b.Cycle == cycle && b.SourceID == sourceID })
	if i < 0 {
		return nil, 0, nil
	}
	var refs []conversation.PlanEvidenceRef
	for _, entry := range journal.Entries {
		if entry.CheckoutID == bindings[i].ID && entry.Generation <= generation {
			if _, err := s.loadPlanEvidence(ctx, entry.Ref.Digest); err != nil {
				return nil, 0, err
			}
			refs = append(refs, entry.Ref)
		}
	}
	return refs, journal.Omitted, nil
}

type investigationDelta struct {
	digest, data, note string
	archive            []byte
	omitted            int
	exact              bool
}

func investigationEvidenceDelta(delta worktree.PlanDelta) investigationDelta {
	out := investigationDelta{digest: delta.Digest, exact: true}
	var b strings.Builder
	for _, change := range delta.Changes {
		if len(change.Path)+investigationFileSize(change.Before)+investigationFileSize(change.After) > maxEvidenceExcerptBytes-b.Len() {
			out.exact = false
			break
		}
		line := investigationChangeEvidence(change, false)
		if len(line) > maxEvidenceExcerptBytes-b.Len() {
			out.exact = false
			break
		}
		b.WriteString(line)
	}
	if !out.exact {
		// For larger normal source files, keep a readable excerpt around the
		// first change and the full, lossless snapshot within a separate bound.
		b.Reset()
		for i, change := range delta.Changes {
			if len(change.Path) > maxEvidenceExcerptBytes-b.Len() {
				out.omitted = len(delta.Changes) - i
				break
			}
			line := investigationChangeEvidence(change, true)
			if len(line) > maxEvidenceExcerptBytes-b.Len() {
				out.omitted = len(delta.Changes) - i
				break
			}
			b.WriteString(line)
		}
		out.archive = compressInvestigationDelta(delta.Changes)
		out.exact = len(out.archive) != 0
	}
	out.data = b.String()
	if len(delta.Changes) == 0 {
		out.note = "The measured files match the accepted baseline."
	}
	return out
}

func investigationFileSize(file *worktree.File) int {
	if file == nil {
		return 0
	}
	return len(file.Data)
}

func investigationChangeEvidence(change worktree.Change, excerpt bool) string {
	offset := 0
	if excerpt && change.Before != nil && change.After != nil {
		a, b := change.Before.Data, change.After.Data
		for offset < min(len(a), len(b)) && a[offset] == b[offset] {
			offset++
		}
		offset = max(0, offset-80)
	}
	return fmt.Sprintf("    %q\n%s%s", change.Path,
		investigationFileEvidence("Baseline", change.Before, excerpt, offset),
		investigationFileEvidence("Checkout", change.After, excerpt, offset))
}

func investigationFileEvidence(label string, f *worktree.File, excerpt bool, offset int) string {
	if f == nil {
		return fmt.Sprintf("      %s: absent\n", label)
	}
	header := fmt.Sprintf("      %s: %q, mode %#o, SHA-256 %s\n", label, f.Type, f.Mode, f.Hash)
	if excerpt {
		end := min(len(f.Data), offset+256)
		return header + fmt.Sprintf("      Bytes %d–%d of %d: %q\n", offset, end, len(f.Data), f.Data[offset:end])
	}
	// Preserve exact bytes, including binary/invalid UTF-8, without
	// interpreting any as markup or terminal controls.
	return header + fmt.Sprintf("      Bytes: %q\n", f.Data)
}

type evidenceArchiveBuffer struct{ bytes.Buffer }

func (b *evidenceArchiveBuffer) Write(p []byte) (int, error) {
	if len(p) > maxEvidenceArchiveBytes-b.Len() {
		return 0, errors.New("compressed investigation delta exceeds the evidence limit")
	}
	return b.Buffer.Write(p)
}

func compressInvestigationDelta(changes []worktree.Change) []byte {
	total := 0
	for _, change := range changes {
		total += len(change.Path) + investigationFileSize(change.Before) + investigationFileSize(change.After) + 256
		if total > maxEvidenceArchiveRaw {
			return nil
		}
	}
	var b evidenceArchiveBuffer
	zip, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	writeErr := json.NewEncoder(zip).Encode(changes)
	if closeErr := zip.Close(); writeErr != nil || closeErr != nil {
		return nil
	}
	return b.Bytes()
}

type investigationEvidenceRecord struct {
	ID         string
	Generation uint64
	Baseline   string
	BaseCommit string
	CWD, Args  string
	Source     string
	Started    time.Time
	Finished   *time.Time
	Status     conversation.PlanEvidenceStatus
	Note       string
	Before     investigationDelta
	After      investigationDelta
	Result     *command.Result
}

func (r investigationEvidenceRecord) blob(label string) (conversation.PlanEvidenceRef, []byte) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Investigation command\n\nResult: **%s**. %s\n", r.Status, r.Note)
	fmt.Fprintf(&b, "\n    Record: %s\n    Turn: %d\n    Started: %s\n", r.ID, r.Generation, r.Started.Format(time.RFC3339Nano))
	if r.Finished != nil {
		fmt.Fprintf(&b, "    Result recorded: %s\n", r.Finished.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "    Accepted baseline SHA-256: %s\n    Git base: %q\n", r.Baseline, r.BaseCommit)
	fmt.Fprintf(&b, "    Working directory: %s\n    Argv: %s\n", r.CWD, r.Args)
	if r.Source != "" {
		fmt.Fprintf(&b, "    Command text: %s\n", r.Source)
	}
	b.WriteString("\nThe file review covers accepted project files and non-ignored changes. It does not include disposable ignored output. Quoted values show exact bytes with escapes.\n")
	writeEvidenceDelta(&b, "Before the command", r.Before)
	writeEvidenceDelta(&b, "After the command", r.After)
	if result := r.Result; result != nil {
		fmt.Fprintf(&b, "\n## Process result\n\n    Process status: %q\n", result.OutputDescriptor.Status)
		if result.ExitCode != nil {
			fmt.Fprintf(&b, "    Exit code: %d\n", *result.ExitCode)
		}
		fmt.Fprintf(&b, "    Cancelled: %t; timed out: %t; uncertain: %t\n", result.Cancelled, result.TimedOut, result.Uncertain)
		if result.Error != "" {
			fmt.Fprintf(&b, "    Process error (excerpt): %q\n", investigationBound(result.Error, 512))
		}
		fmt.Fprintf(&b, "\n## Output\n\n    Captured bytes: %d; omitted by the command owner: %d\n", result.OutputDescriptor.TotalBytes, result.OutputDescriptor.OmittedBytes)
		if result.OutputDescriptor.RetentionError != "" {
			fmt.Fprintf(&b, "    Output retention issue: %q\n", investigationBound(result.OutputDescriptor.RetentionError, 512))
		}
		if result.OutputEncoding != "" {
			fmt.Fprintf(&b, "    Encoding: %q\n", investigationBound(result.OutputEncoding, 32))
		}
		output := investigationBound(result.Output, maxEvidenceOutputBytes)
		if len(output) != len(result.Output) {
			fmt.Fprintf(&b, "    Evidence excerpt: first %d of %d presented bytes\n", len(output), len(result.Output))
		}
		fmt.Fprintf(&b, "    %q\n", output)
	}
	writeEvidenceArchive(&b, "Before", r.Before)
	writeEvidenceArchive(&b, "After", r.After)
	body := []byte(b.String())
	return conversation.PlanEvidenceRef{Digest: evidenceDigest(body), Command: label, Status: r.Status}, body
}

func writeEvidenceDelta(b *strings.Builder, heading string, delta investigationDelta) {
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	if delta.digest == "" && delta.note == "" {
		b.WriteString("No verified snapshot was recorded.\n")
		return
	}
	if delta.note != "" {
		fmt.Fprintln(b, delta.note+"\n")
	}
	if delta.digest != "" {
		fmt.Fprintf(b, "    Reviewed delta SHA-256: %s\n", delta.digest)
	}
	b.WriteString(delta.data)
	if delta.omitted != 0 {
		fmt.Fprintf(b, "\n%d changed files are not shown in the excerpt.\n", delta.omitted)
	}
	if !delta.exact && delta.data != "" {
		b.WriteString("\nThe full file changes exceeded the evidence limit and were not retained.\n")
	}
}

func writeEvidenceArchive(b *strings.Builder, name string, delta investigationDelta) {
	if len(delta.archive) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s: Exact file changes\n\nThe file excerpts appear above. This archive preserves the exact changed files. Decode base64, then decompress gzip to read the JSON. The file `data` values are base64.\n\nArchive SHA-256: `%s`\n\n```text\n", name, evidenceDigest(delta.archive))
	encoded := base64.StdEncoding.EncodeToString(delta.archive)
	for len(encoded) > 0 {
		n := min(len(encoded), 76)
		b.WriteString(encoded[:n])
		b.WriteByte('\n')
		encoded = encoded[n:]
	}
	b.WriteString("```\n")
}

func investigationBound(value string, size int) string { return value[:min(len(value), size)] }

func evidenceCommandLabel(command string) string {
	command = strings.Join(strings.Fields(investigationBound(command, 512)), " ")
	quoted := strconv.Quote(command)
	label := quoted[1 : len(quoted)-1]
	if len(label) > 120 {
		// QuoteToASCII is only needed for a clipped label: cut ASCII without
		// splitting a display rune, while the report still holds exact argv.
		quoted = strconv.QuoteToASCII(command)
		label = quoted[1:118] + "…"
	}
	return label
}

func evidenceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validEvidenceDigest(digest string) bool {
	return len(digest) == sha256.Size*2 && strings.Trim(digest, "0123456789abcdef") == ""
}

func (s *Session) loadPlanEvidence(ctx context.Context, digest string) ([]byte, error) {
	if !validEvidenceDigest(digest) {
		return nil, errPlanEvidenceUnavailable
	}
	raw, err := sessiondb.LoadRuntimeState(ctx, s.db, investigationBlobPrefix+digest)
	if err != nil || len(raw) == 0 {
		return nil, errPlanEvidenceUnavailable
	}
	if len(raw) > maxEvidenceRecordBytes || evidenceDigest(raw) != digest {
		return nil, errPlanEvidenceChanged
	}
	return raw, nil
}

func (s *Session) planEvidenceRevision(number int, digest string) (*planReviewRecord, error) {
	record, i := s.findPlanRevision(number)
	if i < 0 || !slices.ContainsFunc(record.Revisions[i].Evidence, func(ref conversation.PlanEvidenceRef) bool { return ref.Digest == digest }) {
		return nil, errPlanEvidenceUnavailable
	}
	return record, nil
}

// ReadPlanEvidence provides verified immutable Markdown to a read-only viewer.
// It never reads an editable plan file or resolves a caller-supplied path.
func (s *Session) ReadPlanEvidence(revision int, digest string) (string, error) {
	finish, err := s.beginPlanReviewOperation()
	if err != nil {
		return "", err
	}
	defer finish()
	if _, err := s.planEvidenceRevision(revision, digest); err != nil {
		return "", err
	}
	data, err := s.loadPlanEvidence(context.Background(), digest)
	return string(data), err
}

// OpenPlanEvidence verifies and materializes one revision's immutable record.
// The result is separate from the plan file and never opens an editable buffer.
func (s *Session) OpenPlanEvidence(revision int, digest string) (string, error) {
	finish, err := s.beginPlanReviewOperation()
	if err != nil {
		return "", err
	}
	defer finish()
	record, err := s.planEvidenceRevision(revision, digest)
	if err != nil {
		return "", err
	}
	return s.materializePlanEvidence(context.Background(), record.dir, digest)
}

func (s *Session) materializePlanEvidence(ctx context.Context, sessionDir, digest string) (string, error) {
	data, err := s.loadPlanEvidence(ctx, digest)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(sessionDir, "plan-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errPlanEvidenceUnavailable
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", errPlanEvidenceUnavailable
	}
	path := filepath.Join(dir, digest+".md")
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := fileio.WriteAtomicNewChecked(path, data, 0400, nil); err != nil && !fileio.WriteCommitted(err) {
			return "", errPlanEvidenceUnavailable
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 {
		return "", errPlanEvidenceChanged
	}
	existing, err := fileio.ReadRegularNoFollow(ctx, path, maxEvidenceRecordBytes)
	if err != nil || evidenceDigest(existing) != digest {
		return "", errPlanEvidenceChanged
	}
	return path, nil
}

func (s *Session) implementationEvidence(ctx context.Context, dir string, revision *planRevisionRecord, text string) (string, error) {
	refs := make([]prompt.PlanImplementationEvidenceRef, 0, len(revision.Evidence))
	for _, ref := range revision.Evidence {
		path, err := s.materializePlanEvidence(ctx, dir, ref.Digest)
		if err != nil {
			return "", err
		}
		refs = append(refs, prompt.PlanImplementationEvidenceRef{Path: path, Digest: ref.Digest, Status: string(ref.Status)})
	}
	return prompt.PlanImplementationEvidence(text, revision.Number, refs), nil
}
