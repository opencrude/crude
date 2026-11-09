package workspace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/textencoding"

	"github.com/go-git/go-git/v5"
)

func encodedFixture(t *testing.T, text string, encoding textencoding.Encoding) []byte {
	t.Helper()
	raw, err := textencoding.EncodeText([]byte(text), encoding)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEncodedGitComparisonGutterAndHunkStageKeepOriginalBytes(t *testing.T) {
	_, wt, service, root := reviewRepository(t, true)
	before := encodedFixture(t, "one\ntwo\nthree\n", textencoding.UTF16LE)
	after := encodedFixture(t, "ONE\ntwo\nTHREE\n", textencoding.UTF16LE)
	reviewWrite(t, root, "encoded.txt", string(before))
	if _, err := wt.Add("encoded.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("encoded source", &git.CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	reviewWrite(t, root, "encoded.txt", string(after))
	comparison := reviewCompare(t, service, "encoded.txt", GitAreaWorking)
	// Semantic control uses JSON as well as the binary core transport. This
	// round trip must not replace non-UTF-8 bytes with U+FFFD.
	wire, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	var transported GitComparison
	if err := json.Unmarshal(wire, &transported); err != nil {
		t.Fatal(err)
	}
	comparison = transported
	if comparison.Binary || !bytes.Equal(comparison.Before, before) || !bytes.Equal(comparison.After, after) {
		t.Fatal("comparison discarded exact encoded bytes")
	}
	b, a, be, ae, err := comparison.TextVersions()
	if err != nil || b != "one\ntwo\nthree\n" || a != "ONE\ntwo\nTHREE\n" || be != textencoding.UTF16LE || ae != be {
		t.Fatalf("comparison text = %q %q %q %q %v", b, a, be, ae, err)
	}
	annotation, err := service.AnnotateFile(t.Context(), filepath.Join(root, "encoded.txt"), a)
	if err != nil || annotation.HeadContent != b || len(annotation.Hunks) != 2 {
		t.Fatalf("gutter baseline was not decoded: %+v %v", annotation, err)
	}
	if err := service.StageHunk(t.Context(), comparison, GitHunkRange{OldStart: 0, OldEnd: 1, NewStart: 0, NewEnd: 1}); err != nil {
		t.Fatal(err)
	}
	index, err := gitStdout(t.Context(), root, nil, "show", ":encoded.txt")
	want := encodedFixture(t, "ONE\ntwo\nthree\n", textencoding.UTF16LE)
	if err != nil || !bytes.Equal(index, want) {
		t.Fatalf("staged wrong bytes: %x %v", index, err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "encoded.txt"))
	if err != nil || !bytes.Equal(actual, after) {
		t.Fatal("hunk staging changed the working file")
	}
}

func TestEncodedExecutionBaselineIntegrationAndUndo(t *testing.T) {
	for _, encodingOnly := range []bool{false, true} {
		name := "text"
		if encodingOnly {
			name = "encoding-only"
		}
		t.Run(name, func(t *testing.T) {
			s, _, root := executionFixture(t)
			before := encodedFixture(t, "one\ntwo\nthree\n", textencoding.UTF16BE)
			executionPut(t, root, "live.txt", string(before))
			owner := document.Owner{ClientID: "human", Epoch: 1}
			snap, err := s.documents.Open(owner, "live.txt")
			if err != nil {
				t.Fatal(err)
			}
			c := executionCreate(t, s)
			baseline, err := os.ReadFile(filepath.Join(c.Root, "live.txt"))
			if err != nil || !bytes.Equal(baseline, before) {
				t.Fatalf("execution baseline transcoded the document: %x %v", baseline, err)
			}
			proposed, want := "one\ntwo\nthree\n", "one\ntwo\nthree\n"
			if !encodingOnly {
				proposed, want = "one\nTWO\nthree\n", "ONE\nTWO\nthree\n"
				documentEdit(t, s.documents, owner, snap.ID, "later", snap.Revision, 0, 3, "ONE")
			}
			executionPut(t, c.Root, "live.txt", string(encodedFixture(t, proposed, textencoding.UTF8BOM)))
			plan := executionReview(t, s, c.ID)
			receipt, err := s.Apply(t.Context(), plan)
			if err != nil {
				t.Fatal(err)
			}
			after := documentText(t, s.documents, snap.ID, want)
			if after.Encoding != textencoding.UTF8BOM || after.SavedEncoding != textencoding.UTF16BE || !after.Dirty {
				t.Fatalf("integration lost an unsaved encoding change: %+v", after)
			}
			if _, err := s.Undo(t.Context(), receipt.OperationID); err != nil {
				t.Fatal(err)
			}
			want = "one\ntwo\nthree\n"
			if !encodingOnly {
				want = "ONE\ntwo\nthree\n"
			}
			undone := documentText(t, s.documents, snap.ID, want)
			if undone.Encoding != textencoding.UTF16BE || encodingOnly && undone.Dirty {
				t.Fatalf("undo lost encoding/savepoint: %+v", undone)
			}
			actual, err := os.ReadFile(filepath.Join(root, "live.txt"))
			if err != nil || !bytes.Equal(actual, before) {
				t.Fatal("accepted document integration wrote the disk file")
			}
		})
	}
}
