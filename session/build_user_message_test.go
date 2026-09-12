package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

// TestBuildUserMessageDeliversFileAttachments guards the whole point of
// attaching files: the referenced content must reach the agent as readable
// text — decoded for text files, metadata+hex-dump summary for binaries
// (never raw garbage bytes, never a dead end that forces the agent into its
// own read tool).
func TestBuildUserMessageDeliversFileAttachments(t *testing.T) {
	dir := t.TempDir()
	database, err := openTestRefDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// prompt_references carries an FK on sessions(id) — seed the parent row.
	if _, err := database.Exec(
		`INSERT INTO sessions (id, project_path, project_name, created_at) VALUES (?, ?, ?, 0)`,
		"s", dir, "test",
	); err != nil {
		t.Fatal(err)
	}

	textPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textPath, []byte("hello attachment\nline two"), 0o600); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binPath, append([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01}, make([]byte, 32)...), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		id       string
		label    string
		filePath string
	}{
		{id: "ref-text", label: "[File1]", filePath: textPath},
		{id: "ref-bin", label: "[File2]", filePath: binPath},
	} {
		ref := sessiondb.Reference{
			ID: tc.id, SessionID: "s", Kind: sessiondb.KindSelectedFile,
			Label: tc.label, FilePath: tc.filePath,
		}
		if err := sessiondb.InsertPromptReference(database, ref); err != nil {
			t.Fatal(err)
		}
	}

	s := &Session{db: database}
	msg := s.buildUserMessage(provider.InputWithRefs(
		"please review", []string{"ref-text", "ref-bin"},
	), "test-model")

	if msg.Content.IsText() {
		t.Fatalf("multipart message expected, got plain text %q", msg.Content.String())
	}

	var all strings.Builder
	for _, part := range msg.Content.Parts() {
		all.WriteString(part.Text)
		all.WriteString("\n")
	}
	rendered := all.String()

	if !strings.Contains(rendered, "--- [File1] ---") || !strings.Contains(rendered, "hello attachment") {
		t.Fatalf("text attachment missing:\n%s", rendered)
	}
	if !strings.Contains(rendered, "--- [File2] ---") {
		t.Fatalf("binary attachment header missing:\n%s", rendered)
	}
	for _, want := range []string{"Binary file (", "Hex dump of first", "execute_command"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("binary summary missing %q:\n%s", want, rendered)
		}
	}
	// Raw control bytes must never leak into provider text parts.
	for _, part := range msg.Content.Parts() {
		if strings.ContainsRune(part.Text, 0x00) {
			t.Fatalf("raw NUL byte leaked into a content part")
		}
	}
}

func TestRuntimeContextRemainsSeparateFromMultipartHumanInput(t *testing.T) {
	user := provider.ChatMessage{
		Role: "user",
		Content: provider.PartsContent([]provider.ContentPart{
			{Type: "text", Text: "inspect"},
			{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64,YWJj"}},
		}),
	}
	runtimeContext := provider.NewTextMessage("user", "<turn_context>visual context</turn_context>")
	runtimeContext.Origin = provider.OriginRuntimeContext
	runtimeContext.Visibility = provider.VisibilityContextOnly
	if !user.Content.HasImage() || strings.Contains(user.Content.ContentDescription(), "visual context") ||
		runtimeContext.IsGenuineUserInput() || runtimeContext.EffectiveVisibility() != provider.VisibilityContextOnly {
		t.Fatalf("human=%#v runtime=%#v", user, runtimeContext)
	}
}

func TestSessionSystemPromptIsByteStableAcrossEnvironmentChanges(t *testing.T) {
	s := &Session{}
	t.Setenv("SHELL", "/bin/first")
	first := s.systemPrompt(true)
	t.Setenv("SHELL", "/bin/second")
	second := s.systemPrompt(true)
	if first != second {
		t.Fatal("one session rebuilt a different cache prefix")
	}
	if solo := s.systemPrompt(false); solo == first {
		t.Fatal("multi-agent mode did not receive its own prompt snapshot")
	}
}

func TestBuildUserMessageDeliversUnknownModelImageBytes(t *testing.T) {
	dir := t.TempDir()
	database, err := openTestRefDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedPromptReferenceSession(t, database, "vision-session", dir)

	imagePath := filepath.Join(dir, "pasted.png")
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := sessiondb.Reference{
		ID: "ref-image", SessionID: "vision-session", Kind: sessiondb.KindPastedImage,
		Label: "[Pasted IMG1]", Mime: "image/png", FilePath: imagePath,
	}
	if err := sessiondb.InsertPromptReference(database, ref); err != nil {
		t.Fatal(err)
	}

	s := &Session{db: database}
	msg, labels := s.buildUserMessageWithDisplay(provider.InputWithRefs(
		"[Pasted IMG1] what is this?", []string{ref.ID},
	), "unknown-vision-alias-for-attachment-test")
	if len(labels) != 1 || labels[0] != ref.Label {
		t.Fatalf("reference labels = %v", labels)
	}
	parts := msg.Content.Parts()
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "what is this?" {
		t.Fatalf("content parts = %+v", parts)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil ||
		!strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image part = %+v", parts[1])
	}
	if strings.Contains(msg.Content.String(), ref.Label) || strings.Contains(msg.Content.String(), imagePath) {
		t.Fatalf("display caption/path leaked into model text: %q", msg.Content.String())
	}
}

func TestBuildUserMessageExpandsPastedTextInsteadOfCaption(t *testing.T) {
	dir := t.TempDir()
	database, err := openTestRefDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedPromptReferenceSession(t, database, "text-session", dir)

	ref := sessiondb.Reference{
		ID: "ref-text", SessionID: "text-session", Kind: sessiondb.KindPastedText,
		Label: "[Pasted Text1]", Content: "first line\nsecond line", Inline: true,
	}
	if err := sessiondb.InsertPromptReference(database, ref); err != nil {
		t.Fatal(err)
	}

	s := &Session{db: database}
	msg, labels := s.buildUserMessageWithDisplay(provider.InputWithRefs(
		"summarize [Pasted Text1]", []string{ref.ID},
	), "text-model")
	if len(labels) != 1 || labels[0] != ref.Label {
		t.Fatalf("reference labels = %v", labels)
	}
	joined := msg.Content.String()
	if !strings.Contains(joined, "summarize") || !strings.Contains(joined, ref.Content) {
		t.Fatalf("pasted payload missing from model content: %q", joined)
	}
	if strings.Contains(joined, ref.Label) {
		t.Fatalf("reference caption leaked into model content: %q", joined)
	}
}

func TestBuildUserMessageKnownNonVisionDoesNotExposeLocalImagePath(t *testing.T) {
	dir := t.TempDir()
	database, err := openTestRefDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedPromptReferenceSession(t, database, "nonvision-session", dir)

	imagePath := filepath.Join(dir, "private.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := sessiondb.Reference{
		ID: "ref-image", SessionID: "nonvision-session", Kind: sessiondb.KindPastedImage,
		Label: "[Pasted IMG1]", Mime: "image/png", FilePath: imagePath,
	}
	if err := sessiondb.InsertPromptReference(database, ref); err != nil {
		t.Fatal(err)
	}

	const model = "known-nonvision-attachment-test"
	provider.SetModelCap(model, false, true)
	msg := (&Session{db: database}).buildUserMessage(provider.InputWithRefs(
		"inspect [Pasted IMG1]", []string{ref.ID},
	), model)
	if msg.Content.HasImage() {
		t.Fatal("known non-vision model received an image part")
	}
	text := msg.Content.String()
	if strings.Contains(text, imagePath) {
		t.Fatalf("local image path leaked into model context: %q", text)
	}
	if !strings.Contains(text, "image omitted") {
		t.Fatalf("missing non-vision explanation: %q", text)
	}
}

func seedPromptReferenceSession(t *testing.T, database *sql.DB, id, dir string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO sessions (id, project_path, project_name, created_at) VALUES (?, ?, ?, 0)`,
		id, dir, "test",
	); err != nil {
		t.Fatal(err)
	}
}

func openTestRefDB(dir string) (*sql.DB, error) {
	return sessiondb.OpenAndMigrate(filepath.Join(dir, "session.db"))
}
