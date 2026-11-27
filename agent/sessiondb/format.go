package sessiondb

import (
	"context"
	"crude/agent/provider"
	"encoding/json"
	"fmt"
	"time"

	"crude/internal/dataformat"
)

// Format is the session database format. The data update runs its steps
// before the core opens any session; Migrate opens only the current version.
//
//   - Version 1 covers v0.1.1 through v0.8.17: the messages of
//     an unfinished run belong to its recovery incident (agent_recovery_tail).
//   - Version 2 gives them to the runtime agent (agent_message_lists), so a new
//     run extends its agent's list instead of writing the history again.
//   - Version 3 returns to recall the workspace memories that maintenance
//     archived only for their age or for the size of the store.
//   - Version 4 records who wrote each context checkpoint (writer): the
//     provider's own compaction, Errand or the agent's model.
//
// A workspace database holds this same session schema beside its workspace
// and memory tables (workspace.openWorkspaceDBAt creates it with Migrate), so
// it has this format too, and a step can change those tables where they exist.
// Steps are frozen: they use the tables of their own time, never current code.
var Format = dataformat.Format{
	Name:     "session",
	Current:  4,
	Classify: classifyFormat,
	Steps: []dataformat.Step{
		{To: 2, Title: "Give the messages of unfinished runs to their agents", Apply: giveRecoveryListsToAgents},
		{To: 3, Title: "Return hidden workspace memories to recall", Apply: returnArchivedMemories},
		{To: 4, Title: "Record who wrote each context checkpoint", Apply: recordCheckpointWriters},
	},
}

// recordCheckpointWriters adds the writer of each checkpoint. Checkpoints from
// before this version name no writer; the transcript shows them as before.
func recordCheckpointWriters(ctx context.Context, u dataformat.Unit) error {
	_, err := u.Q.ExecContext(ctx, `ALTER TABLE context_compactions ADD COLUMN writer TEXT NOT NULL DEFAULT ''`)
	u.Progress(1)
	return err
}

func hasTable(ctx context.Context, q dataformat.Querier, name string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&found)
	return found, err
}

// classifyFormat names the version of a session database saved before formats
// had versions. Unknown layouts are left unchanged.
func classifyFormat(ctx context.Context, q dataformat.Querier) (int, error) {
	return dataformat.ClassifyTables(ctx, q, `'agent_context_generations','agent_context_state','agent_context_tail','agent_message_list_rows','agent_message_lists','agent_recoveries','agent_recovery_lists','agent_recovery_tail','agent_turns','collaboration_admissions','context_compactions','file_claims','immutable_message_identities','messages','plan_events','plan_steps','plans','prompt_references','session_agent_settings','session_capabilities','session_flags','session_goal','session_goal_handover','session_runtime_state','session_status_rows','sessions','sub_agent_results','usage_requests'`, releasedLayouts)
}

// Digests of the owned table columns, defaults and keys in the release tags.
// Do not stamp a database from an unrecognized layout on the strength of a single table.
var releasedLayouts = map[string]int{
	// Development stores with the revision/origin defaults from ALTER TABLE.
	"bf5296d908f3830f71ddbec053269405f254e0fbcc17ca01ad00d37e309abe5a": 1,
	"ea3408539d70a25aa041d79a4cfab0d974fab133d2f7e668702412377e82ecc0": 1,
	// Before session capabilities, and before publication revisions, respectively.
	"0113b103309be60a96cd632f9588071a646d56e543e2832fa308a2a59903ee4c": 1,
	"10dd73c80a0e3585826fe0d82c6920c4489519e0890048ddfa51a970d75f4795": 1,

	"693a423e5471eeab17761049c5706e4cee447572e6706d8c94efde7d4c6269c4": 1, // v0.1.1 through v0.1.1
	"5aa7fb938be594e907710871dbdd9e67ca136c2615f12393f1cb5fa4ed4a7bdc": 1, // v0.2.0 through v0.2.2
	"77e0318c6ab28c4a4dde56831ab13e74bfaba282365ef4f93c1135701eb5bf20": 1, // v0.2.3 through v0.8.17
	"0f38dd00ada24828b609163061d57887f6c62be4709ad832d66c8574859226c9": 2, // format-2 through format-2
}

// Version 2 tables, as they were when the step was written.
const agentMessageListsV2 = `
CREATE TABLE agent_message_lists (
    list_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id       TEXT NOT NULL,
    field          TEXT NOT NULL,
    version        INTEGER NOT NULL
);
CREATE INDEX idx_agent_message_lists_agent
    ON agent_message_lists(agent_id, field, list_id);
CREATE TABLE agent_message_list_rows (
    list_id        INTEGER NOT NULL,
    position       INTEGER NOT NULL,
    message_json   BLOB NOT NULL,
    PRIMARY KEY (list_id, position),
    FOREIGN KEY (list_id) REFERENCES agent_message_lists(list_id) ON DELETE CASCADE
);
CREATE TABLE agent_recovery_lists (
    incident_id    TEXT NOT NULL,
    field          TEXT NOT NULL,
    list_id        INTEGER NOT NULL,
    version        INTEGER NOT NULL,
    PRIMARY KEY (incident_id, field),
    FOREIGN KEY (incident_id) REFERENCES agent_recoveries(incident_id) ON DELETE CASCADE
);
CREATE INDEX idx_agent_recovery_lists_list
    ON agent_recovery_lists(list_id);
`

// giveRecoveryListsToAgents moves each incident's list rows into a list of its
// own, owned by the incident's agent, in incident update order, so each
// agent's newest incident has its newest list. Lists keep their versions. The
// moved rows are compared byte for byte with the old rows before the old
// tables are dropped.
func giveRecoveryListsToAgents(ctx context.Context, u dataformat.Unit) error {
	q := u.Q
	if err := prepareDevelopmentTables(ctx, q); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, releaseAdditions); err != nil {
		return err
	}
	var orphans int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM agent_recovery_tail WHERE incident_id NOT IN (SELECT incident_id FROM agent_recoveries)`).Scan(&orphans); err != nil {
		return err
	}
	if orphans != 0 {
		return fmt.Errorf("%d recovery messages belong to no recovery incident", orphans)
	}
	type field struct {
		incident, agent, name string
		version               int64
	}
	rows, err := q.QueryContext(ctx, `SELECT r.incident_id, r.agent_id, f.field,
		COALESCE((SELECT l.version FROM agent_recovery_lists l WHERE l.incident_id=r.incident_id AND l.field=f.field), 1)
		FROM agent_recoveries r
		JOIN (SELECT incident_id, field FROM agent_recovery_lists
		      UNION SELECT DISTINCT incident_id, field FROM agent_recovery_tail) f ON f.incident_id = r.incident_id
		ORDER BY r.updated_at, r.incident_id, f.field`)
	if err != nil {
		return err
	}
	var fields []field
	for rows.Next() {
		var f field
		if err := rows.Scan(&f.incident, &f.agent, &f.name, &f.version); err != nil {
			_ = rows.Close()
			return err
		}
		fields = append(fields, f)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `ALTER TABLE agent_recovery_lists RENAME TO update_old_recovery_lists`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, agentMessageListsV2); err != nil {
		return err
	}
	for i, f := range fields {
		result, err := q.ExecContext(ctx, `INSERT INTO agent_message_lists(agent_id, field, version) VALUES(?, ?, ?)`, f.agent, f.name, f.version)
		if err != nil {
			return err
		}
		list, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO agent_message_list_rows(list_id, position, message_json)
			SELECT ?, position, message_json FROM agent_recovery_tail WHERE incident_id=? AND field=? ORDER BY position`, list, f.incident, f.name); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO agent_recovery_lists(incident_id, field, list_id, version) VALUES(?, ?, ?, ?)`, f.incident, f.name, list, f.version); err != nil {
			return err
		}
		u.Progress(float64(i+1) / float64(len(fields)+1))
	}
	var old, moved, same int64
	if err := q.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM agent_recovery_tail),
		(SELECT count(*) FROM agent_message_list_rows),
		(SELECT count(*) FROM agent_recovery_tail t
		   JOIN agent_recovery_lists l ON l.incident_id=t.incident_id AND l.field=t.field
		   JOIN agent_message_list_rows r ON r.list_id=l.list_id AND r.position=t.position
		  WHERE r.message_json = t.message_json)`).Scan(&old, &moved, &same); err != nil {
		return err
	}
	if old != moved || old != same {
		return fmt.Errorf("moved %d of %d messages, %d unchanged; nothing changed", moved, old, same)
	}
	if _, err := q.ExecContext(ctx, `DROP TABLE agent_recovery_tail; DROP TABLE update_old_recovery_lists`); err != nil {
		return err
	}
	problems, err := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	bad := problems.Next()
	if err := problems.Close(); err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("recovery lists have broken references; nothing changed")
	}
	return nil
}

// returnArchivedMemories reactivates every memory whose latest change is an
// archive by the retired count cap or age decay of memory maintenance. A
// memory that changed after that archive, or that was archived for another
// reason, stays as it is. Each return is a new revision with its own journal
// entry, as an archive was.
func returnArchivedMemories(ctx context.Context, u dataformat.Unit) error {
	q := u.Q
	for _, table := range []string{"workspace_memories", "memory_revisions", "memory_journal"} {
		found, err := hasTable(ctx, q, table)
		if err != nil || !found {
			return err
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT m.id, m.revision, j.detail FROM workspace_memories m
		JOIN memory_journal j ON j.id = (SELECT max(id) FROM memory_journal WHERE memory_id = m.id)
		WHERE m.status = 'archived' AND j.op = 'archive_reason'
		  AND j.detail IN ('capped: active set over limit', 'decayed: no recent usefulness evidence')`)
	if err != nil {
		return err
	}
	type archived struct {
		id       string
		revision int64
		reason   string
	}
	var records []archived
	for rows.Next() {
		var r archived
		if err := rows.Scan(&r.id, &r.revision, &r.reason); err != nil {
			_ = rows.Close()
			return err
		}
		records = append(records, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	now := time.Now().UnixNano()
	for i, r := range records {
		next := r.revision + 1
		result, err := q.ExecContext(ctx, `UPDATE workspace_memories SET revision = ?, status = 'active', actor = 'update' WHERE id = ? AND revision = ? AND status = 'archived'`, next, r.id, r.revision)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("memory %s changed during the update", r.id)
		}
		if saved, err := q.ExecContext(ctx, `INSERT INTO memory_revisions(memory_id, revision, record, session_id, op)
			SELECT memory_id, ?, json_set(record, '$.Revision', ?, '$.Status', 'active', '$.Actor', 'update'), '', 'restore'
			FROM memory_revisions WHERE memory_id = ? AND revision = ?`, next, next, r.id, r.revision); err != nil {
			return err
		} else if n, err := saved.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("memory %s has no matching revision to restore", r.id)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO memory_journal(at, actor, op, memory_id, session_id, detail) VALUES(?, 'update', 'restore', ?, '', ?)`,
			now, r.id, fmt.Sprintf("revision:%d returned to recall; it was archived only by retired maintenance (%s)", next, r.reason)); err != nil {
			return err
		}
		u.Progress(float64(i+1) / float64(len(records)))
	}
	return nil
}

// Additions made between the earliest release and this step's input schema.
const releaseAdditions = `
CREATE INDEX IF NOT EXISTS idx_messages_public_run ON messages(json_extract(display_meta,'$.run_id'),sequence_num)
    WHERE visibility='conversation' AND json_valid(display_meta);
CREATE TABLE IF NOT EXISTS agent_context_tail (
    session_id     TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    position       INTEGER NOT NULL,
    message_json   BLOB NOT NULL,
    PRIMARY KEY (session_id, agent_id, position),
    FOREIGN KEY (session_id, agent_id) REFERENCES agent_context_state(session_id, agent_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS agent_recovery_tail (
    incident_id    TEXT NOT NULL,
    field          TEXT NOT NULL,
    position       INTEGER NOT NULL,
    message_json   BLOB NOT NULL,
    PRIMARY KEY (incident_id, field, position),
    FOREIGN KEY (incident_id) REFERENCES agent_recoveries(incident_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS agent_recovery_lists (
    incident_id    TEXT NOT NULL,
    field          TEXT NOT NULL,
    version        INTEGER NOT NULL,
    PRIMARY KEY (incident_id, field),
    FOREIGN KEY (incident_id) REFERENCES agent_recoveries(incident_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS session_status_rows (
    id         TEXT PRIMARY KEY,
    phase      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session_goal (
    session_id   TEXT PRIMARY KEY,
    goal_id      TEXT NOT NULL,
    objective    TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('active','paused','blocked','budget_limited','complete')),
    reason       TEXT NOT NULL DEFAULT '',
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    token_budget INTEGER NOT NULL DEFAULT 0,
    time_used_ms INTEGER NOT NULL DEFAULT 0,
    waiting      INTEGER NOT NULL DEFAULT 0,
    verifier_id  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS session_goal_handover (
    session_id TEXT PRIMARY KEY,
    lifecycle_epoch INTEGER NOT NULL,
    record BLOB NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
`

// These pre-tag layouts are admitted only by their complete column signature.
// Defaults do not reconstruct history: establish a revision baseline once,
// leave the original transcript and all context arrays untouched, and populate
// missing immutable identities only from a full, attributed context record.
func prepareDevelopmentTables(ctx context.Context, q dataformat.Querier) error {
	for _, c := range []struct{ table, column, ddl string }{{"session_flags", "code_intelligence", "INTEGER"}, {"session_flags", "max_sub_agents", "INTEGER"}, {"agent_context_state", "revision", "INTEGER NOT NULL DEFAULT 1"}, {"agent_recoveries", "context_revision", "INTEGER NOT NULL DEFAULT 0"}} {
		var found bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, c.table, c.column).Scan(&found); err != nil {
			return err
		}
		if !found {
			if _, err := q.ExecContext(ctx, `ALTER TABLE `+c.table+` ADD COLUMN `+c.column+` `+c.ddl); err != nil {
				return err
			}
		}
	}
	if _, err := q.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS session_capabilities(session_id TEXT NOT NULL,capability_id TEXT NOT NULL,PRIMARY KEY(session_id,capability_id),FOREIGN KEY(session_id) REFERENCES sessions(id))`); err != nil {
		return err
	}
	exists, err := hasTable(ctx, q, "immutable_message_identities")
	if err != nil || exists {
		return err
	}
	if _, err = q.ExecContext(ctx, `CREATE TABLE immutable_message_identities(internal_id TEXT PRIMARY KEY,session_id TEXT NOT NULL,agent_id TEXT NOT NULL,message_json BLOB NOT NULL,created_at INTEGER NOT NULL,FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE)`); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT h.session_id,h.agent_id,j.value,h.updated_at FROM agent_context_state h,json_each(h.messages_json) j UNION ALL SELECT h.session_id,h.agent_id,j.value,h.created_at FROM agent_context_generations h,json_each(h.messages_json) j UNION ALL SELECT h.session_id,h.context_owner_id,j.value,h.created_at FROM agent_recoveries h,json_each(h.archive_json) j UNION ALL SELECT h.session_id,h.context_owner_id,j.value,h.updated_at FROM agent_recoveries h,json_each(h.active_context_json) j`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var session, owner string
		var raw []byte
		var at int64
		if err := rows.Scan(&session, &owner, &raw, &at); err != nil {
			return err
		}
		var msg provider.ChatMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return err
		}
		if msg.InternalID == "" {
			continue
		}
		if owner == "" {
			return fmt.Errorf("missing owner for saved message %s", msg.InternalID)
		}
		payload, err := immutableMessagePayload(msg)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO immutable_message_identities VALUES(?,?,?,?,?) ON CONFLICT(internal_id) DO NOTHING`, msg.InternalID, session, owner, payload, at); err != nil {
			return err
		}
		var same bool
		if err := q.QueryRowContext(ctx, `SELECT session_id=? AND agent_id=? AND message_json=? FROM immutable_message_identities WHERE internal_id=?`, session, owner, payload, msg.InternalID).Scan(&same); err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("conflicting preimages for saved message %s", msg.InternalID)
		}
	}
	return rows.Err()
}
