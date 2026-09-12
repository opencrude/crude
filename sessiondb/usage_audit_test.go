package sessiondb

import (
	"bufio"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Explicit, read-only reconciliation. Only counters are printed; no prompts,
// request bodies, credentials, or tool output are emitted or transmitted.
func TestUsageArchiveReconciliation(t *testing.T) {
	path := os.Getenv("CRUDE_TEST_USAGE_ARCHIVE")
	if path == "" {
		t.Skip("set CRUDE_TEST_USAGE_ARCHIVE to a session DB")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count, prompt, completion, cached, overwritten int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COALESCE(SUM(cached_tokens),0), COALESCE(SUM(updated_at-created_at>1000000000),0) FROM usage_requests`).Scan(&count, &prompt, &completion, &cached, &overwritten); err != nil {
		t.Fatal(err)
	}
	t.Logf("persisted: requests=%d input=%d output=%d cache_read=%d reused_rows=%d", count, prompt, completion, cached, overwritten)
	log, err := os.Open(filepath.Join(strings.TrimSuffix(path, ".db"), "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	pattern := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d+ INFO \[LLM\] response model=\S+ tool_calls=\d+ prompt_tokens=(\d+) completion_tokens=(\d+) cached_tokens=(\d+) latency_ms=\d+`)
	var responses, input, output, cache int64
	var latestInput int64
	scanner := bufio.NewScanner(log)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		match := pattern.FindSubmatch(scanner.Bytes())
		if match == nil {
			continue
		}
		responses++
		n, _ := strconv.ParseInt(string(match[1]), 10, 64)
		input += n
		latestInput = n
		n, _ = strconv.ParseInt(string(match[2]), 10, 64)
		output += n
		n, _ = strconv.ParseInt(string(match[3]), 10, 64)
		cache += n
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("completed-response log: requests=%d input=%d output=%d cache_read=%d latest_input=%d", responses, input, output, cache, latestInput)
	// These can include auxiliary responses and exclude partial failures; the
	// output is forensic evidence, not a blanket equality assertion.
}
