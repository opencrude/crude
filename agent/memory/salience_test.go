package memory

import (
	"fmt"
	"strings"
	"testing"
)

// The mid-turn ranker's lexical query while an agent applies one edit after
// another on a task about something else entirely. The user had asked more
// than once for multi-file edits to be done with inline Python.
const editingSituation = `edit_file C:/Users/dev/Desktop/crude/agent/session/command_output.go (success): Replaced 1 occurrence in C:/Users/dev/Desktop/crude/agent/session/command_output.go
edit_file C:/Users/dev/Desktop/crude/agent/capability/service.go (success): Replaced 1 occurrence
edit_file C:/Users/dev/Desktop/crude/agent/capability/authorization.go (success): Replaced 1 occurrence
edit_file C:/Users/dev/Desktop/crude/agent/turnloop/tool_exec.go (success): Replaced 1 occurrence
its still very bad, we shouldnt show this progress bar like this, the capability activation probably will always take considerable amounts of time, so please make these stuff async from the get go, this width calculation is very low effort, crude should be intelligent enough to tweak all of the present rows widths automatically`

func topicalCorpus(t *testing.T, store *Store) {
	t.Helper()
	for i, topic := range []string{
		"Capability activation must reuse the async command lifecycle with a progress bar",
		"Activation parity covers skills, sleep and wait latches and agent wake-ups",
		"Background jobs deck needs aligned Output and Stop controls",
		"Progress bar animation must not lag on the idle clock",
		"Capability search must find registry servers by keyword",
		"Session command output keeps retained bytes under a handle",
		"Tool execution reports progress phases to the conversation",
		"Async commands promote after a threshold and report outcomes",
		"Authorization binds an exact launch preimage to the run",
		"Service settings publication revokes stale task authority",
		"Width calculations use the shared visible width profile",
		"Present rows in the chat with consistent operand ink",
		"Activation jobs report measured download bytes",
		"Crude should keep startup durable when settings are wrong",
	} {
		if _, err := store.Remember(t.Context(), Record{Kind: KindPreference, Title: topic,
			Content: fmt.Sprintf("%s. Recorded while working on crude agent session capability code (%d).", topic, i), Importance: 60, Source: SourceTool}); err != nil {
			t.Fatal(err)
		}
	}
}

func statement(session, message, text string) Evidence {
	return Evidence{SessionID: session, MessageID: message, Kind: EvidenceUser, Summary: text, Outcome: OutcomeUnmeasured}
}

func recalledTitles(t *testing.T, store *Store, query string, paths []string, limit int) []string {
	t.Helper()
	records, err := store.Recall(t.Context(), RecallQuery{Text: query, ActivePaths: paths, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, record := range records {
		titles = append(titles, record.Title)
	}
	return titles
}

// The ranking model can only choose from the candidates retrieval gives it.
// Words such as "edits" must meet the "edit" of the agent's own actions, long
// paths must not crowd out the query, and knowledge the user restated must
// stay in view though today's task is about something else.
func TestRestatedWorkflowPreferenceReachesTheRankerWhileEditing(t *testing.T) {
	store := openTestStore(t)
	topicalCorpus(t, store)
	const title = "Make code edits with inline Python for speed"
	if _, err := store.Remember(t.Context(), Record{Kind: KindPreference, Title: title,
		Content:     "When applying many small fixes, the user asks for edits done with inline Python rather than slow one-by-one tool calls.",
		AppliesWhen: "Applying multi-file or repetitive edits", Importance: 48, Source: SourceUser,
		Evidence: []Evidence{statement("s1", "m1", "do these edits with inline python"), statement("s2", "m7", "use inline python to speed up edits")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Remember(t.Context(), Record{Kind: KindPreference, Title: "Slash suggestions are mouse-selectable",
		Content: "Clicking a slash command suggestion must activate it.", Importance: 60, Source: SourceUser,
		Evidence: []Evidence{statement("s3", "m2", "slash menu must accept clicks")}}); err != nil {
		t.Fatal(err)
	}
	paths := []string{"agent/session/command_output.go", "agent/capability/service.go", "agent/capability/authorization.go", "agent/turnloop/tool_exec.go"}
	titles := recalledTitles(t, store, editingSituation, paths, 8)
	if !strings.Contains(strings.Join(titles, "\n"), title) {
		t.Fatalf("a twice-stated editing preference never reached the ranking model:\n%s", strings.Join(titles, "\n"))
	}
	if strings.Contains(strings.Join(titles, "\n"), "Slash suggestions") {
		t.Fatalf("knowledge stated once and unrelated to the situation was promoted:\n%s", strings.Join(titles, "\n"))
	}
}

func TestStemmedRecallMatchesWordForms(t *testing.T) {
	store := openTestStore(t)
	for _, record := range []Record{
		{Kind: KindConvention, Title: "Batch repetitive edits", Content: "Editing many files is faster as one scripted change."},
		{Kind: KindFact, Title: "Deploy notes", Content: "Deploy from the release branch."},
	} {
		if _, err := store.Remember(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	// One literal match keeps full-text search from falling back to substring
	// scanning, so only stemming can find the inflected record.
	if titles := recalledTitles(t, store, "edit deploy", nil, 4); len(titles) != 2 {
		t.Fatalf("query word did not meet its inflected forms: %v", titles)
	}
}

// Context paths fill only the room the request leaves.
func TestMatchQueryKeepsRequestWordsBesideLongContext(t *testing.T) {
	var paths []string
	for i := range 80 {
		paths = append(paths, fmt.Sprintf("internal/very/long/generated/directory%03d/component_implementation_file.go", i))
	}
	query := BuildMatchQuery("edit python", paths...)
	for _, word := range []string{`"edit"`, `"python"`} {
		if !strings.Contains(query, word) {
			t.Fatalf("context displaced %s: %s", word, query)
		}
	}
	if terms := strings.Count(query, " OR ") + 1; terms > maxQueryTokens {
		t.Fatalf("query has %d terms", terms)
	}
}

// A restatement strengthens the record that holds the knowledge: one audited
// revision with the new statement, never a duplicate, and never twice for the
// same message.
func TestRestatementStrengthensExistingKnowledge(t *testing.T) {
	store := openTestStore(t)
	first := userEvidence(t, store, "earlier")
	record, err := store.Remember(t.Context(), Record{Kind: KindPreference, Title: "Make code edits with inline Python for speed",
		Content: "Edits are done with inline Python.", Source: SourceAuto, SourceSessionID: "earlier", Evidence: first})
	if err != nil {
		t.Fatal(err)
	}
	again := statement("later", "human-9", "i told you multiple times, use inline python for edits")
	if err := store.RecordTurn(t.Context(), TurnRecord{SessionID: "later", Turn: 1, UserText: again.Summary,
		Observations: []Observation{{Evidence: again, Turn: 1}}}); err != nil {
		t.Fatal(err)
	}
	result, err := ParseExtraction(`{"memories":[{"kind":"preference","title":"make code edits with inline python for speed","content":"Use inline Python.","evidence":[0]}],"restated":[{"id":"` + record.ID + `","evidence":[0]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	observations := []Observation{{Evidence: again, Turn: 1}}
	generation, err := store.KnowledgeGeneration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch := ExtractionBatch{SessionID: "later", ThroughTurn: 1, ExpectedGeneration: generation,
		Records:        GroundExtraction(result, observations, "later", "test"),
		Reinforcements: GroundRestatements(result, observations, "later")}
	commit, err := store.CommitExtraction(t.Context(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Saved) != 0 || len(commit.Reinforced) != 1 {
		t.Fatalf("restatement saved %d duplicates and reinforced %d records", len(commit.Saved), len(commit.Reinforced))
	}
	got, err := store.Get(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != record.Revision+1 || Statements(got) != 2 || got.Content != record.Content || got.UpdatedAt <= record.UpdatedAt {
		t.Fatalf("restatement did not strengthen the record: %+v", got)
	}
	if count, _ := store.CountByStatus(t.Context(), StatusActive); count != 1 {
		t.Fatalf("restatement created %d records", count)
	}
	revisions, err := store.Revisions(t.Context(), record.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("reinforcement is not an audited revision: %v %v", revisions, err)
	}
}

// An index whose definition changed is rebuilt from its source, so existing
// knowledge stays searchable with the new tokenizer.
func TestChangedIndexDefinitionIsRebuiltFromSource(t *testing.T) {
	db := openTestDB(t)
	store, err := Attach(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Remember(t.Context(), Record{Kind: KindFact, Title: "Deploys need the edits branch", Content: "Releases are cut after edits land."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE workspace_memories_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE workspace_memories_fts USING fts5(id UNINDEXED, title, content, entities, norm)`); err != nil {
		t.Fatal(err)
	}
	if store, err = Attach(db); err != nil {
		t.Fatal(err)
	}
	if !store.FTSEnabled() {
		t.Fatal("rebuild disabled full-text search")
	}
	if titles := recalledTitles(t, store, "edit", nil, 4); len(titles) != 1 {
		t.Fatalf("rebuilt index lost existing knowledge or its stemming: %v", titles)
	}
}
