package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"

	"crude/provider"
	ttypes "crude/types"
	"crude/types/attachment"
	"crude/types/reasoning"
)

const ConversationSnapshotVersion = 1

type DisplayMeta struct {
	SnapshotVersion int                          `json:"snapshot_version,omitempty"`
	Snapshots       []ttypes.ConversationMessage `json:"snapshots,omitempty"`
	ContextOnly     bool                         `json:"context_only,omitempty"`
	TurnGeneration  uint64                       `json:"turn_generation,omitempty"`
	ProviderID      string                       `json:"provider_id,omitempty"`
	AccountID       string                       `json:"account_id,omitempty"`
	ModelID         string                       `json:"model_id,omitempty"`

	// Writer inputs that session canonicalization projects into Snapshots;
	// hydration reads only Snapshots. DisplayRole also marks assignment and
	// hidden runtime-context rows.
	DisplayRole     string              `json:"display_role,omitempty"`
	DisplayContent  string              `json:"display_content,omitempty"`
	ReferenceLabels []string            `json:"reference_labels,omitempty"`
	Attachments     []attachment.Target `json:"attachments,omitempty"`
	ToolName        string              `json:"tool_name,omitempty"`
	ToolArgs        string              `json:"tool_args,omitempty"`
	ToolStatus      string              `json:"tool_status,omitempty"`
	PermTag         string              `json:"perm_tag,omitempty"`
	ProgressLabel   string              `json:"progress_label,omitempty"`
	DurationMs      int64               `json:"duration_ms,omitempty"`
	EstTokens       int                 `json:"est_tokens,omitempty"`
	LinesAdded      int                 `json:"lines_added,omitempty"`
	LinesRemoved    int                 `json:"lines_removed,omitempty"`
	SubPaneID       string              `json:"sub_pane_id,omitempty"`
}

type MessageRow struct {
	Role             string
	Origin           string
	Visibility       string
	InternalID       sql.NullString
	Collaboration    sql.NullString
	Content          sql.NullString
	ContentType      sql.NullString
	ReasoningContent sql.NullString
	Reasoning        sql.NullString
	ToolCalls        sql.NullString
	ToolName         sql.NullString
	ToolResult       sql.NullString
	ToolCallID       sql.NullString
	ToolError        int
	ProviderItems    sql.NullString
	CacheControl     sql.NullString
	TokenCount       int
	SequenceNum      int
	TurnNumber       int
	AgentID          sql.NullString
	CreatedAt        int64
	DisplayMeta      sql.NullString
}

type MessageWithMeta struct {
	Msg       provider.ChatMessage
	Meta      DisplayMeta
	Turn      int
	CreatedAt int64
}

type persistedMessage struct {
	Message   provider.ChatMessage
	Meta      DisplayMeta
	Turn      int
	CreatedAt int64
	AgentID   string
}

func ReplaceMessages(db *sql.DB, sessionID string, msgs []provider.ChatMessage, turnNumbers []int) (int, error) {
	return ReplaceMessagesWithMeta(db, sessionID, msgs, turnNumbers, nil)
}

func ReplaceMessagesWithMeta(db *sql.DB, sessionID string, msgs []provider.ChatMessage, turnNumbers []int, metas []DisplayMeta) (int, error) {
	defer sessionWrites.Since("replace messages", time.Now())
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("DELETE FROM messages"); err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages delete: %w", err)
	}

	maxTurn := 0
	for _, t := range turnNumbers {
		if t > maxTurn {
			maxTurn = t
		}
	}

	if len(msgs) == 0 {
		if _, err := tx.Exec(`
			UPDATE sessions SET message_count = 0, turn_count = ? WHERE id = ?
		`, maxTurn, sessionID); err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages update session: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages commit: %w", err)
		}
		return 0, nil
	}

	stmt, err := tx.Prepare(`
		INSERT INTO messages (
			role, origin, visibility, internal_id, collaboration_json, content, content_type, reasoning_content, reasoning,
			tool_calls, tool_name, tool_result, tool_call_id,
			tool_error, provider_items, cache_control, token_count, sequence_num, turn_number,
			agent_id, created_at, display_meta
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UnixNano()

	for i, m := range msgs {
		var toolCallsJSON []byte
		if len(m.ToolCalls) > 0 {
			toolCallsJSON, err = json.Marshal(m.ToolCalls)
			if err != nil {
				return 0, fmt.Errorf("sessiondb: marshal tool calls seq=%d: %w", i, err)
			}
		}

		var cacheControlJSON []byte
		if m.CacheControl != nil {
			cacheControlJSON, err = json.Marshal(m.CacheControl)
			if err != nil {
				return 0, fmt.Errorf("sessiondb: marshal cache control seq=%d: %w", i, err)
			}
		}
		var providerItemsJSON []byte
		if len(m.ProviderItems) > 0 {
			providerItemsJSON, err = json.Marshal(m.ProviderItems)
			if err != nil {
				return 0, fmt.Errorf("sessiondb: marshal provider items seq=%d: %w", i, err)
			}
		}
		var collaborationJSON []byte
		if m.Collaboration != nil {
			collaborationJSON, err = json.Marshal(m.Collaboration)
			if err != nil {
				return 0, fmt.Errorf("sessiondb: marshal collaboration seq=%d: %w", i, err)
			}
		}

		contentStr := m.Content.String()
		toolResult := ""
		if m.Role == "tool" {
			toolResult = contentStr
			contentStr = ""
		}

		var dbContent string
		var contentType string
		if m.Content.IsText() {
			dbContent = contentStr
			contentType = "text"
		} else {
			dbContent = m.Content.MultipartJSON()
			contentType = "multipart"
		}

		turn := 0
		if i < len(turnNumbers) {
			turn = turnNumbers[i]
		}

		var metaJSON sql.NullString
		if i < len(metas) {
			if b, err := json.Marshal(metas[i]); err == nil && len(b) > 2 {
				metaJSON = sql.NullString{String: string(b), Valid: true}
			}
		}

		_, err := stmt.Exec(
			m.Role,
			m.EffectiveOrigin(),
			m.EffectiveVisibility(),
			nullString(m.InternalID),
			nullBytes(collaborationJSON),
			nullString(dbContent),
			nullString(contentType),
			nullString(m.ReasoningContent),
			nullString(m.Reasoning),
			nullBytes(toolCallsJSON),
			nullString(""),
			nullString(toolResult),
			nullString(m.ToolCallID),
			m.ToolError,
			nullBytes(providerItemsJSON),
			nullBytes(cacheControlJSON),
			provider.EstimateMessageTokens(m, ""),
			i,
			turn,
			nil,
			now,
			metaJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages exec seq=%d: %w", i, err)
		}
	}

	msgCount := len(msgs)
	_, err = tx.Exec(`
		UPDATE sessions SET message_count = ?, turn_count = ? WHERE id = ?
	`, msgCount, maxTurn, sessionID)
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages update session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages commit: %w", err)
	}

	return msgCount, nil
}

func AppendMessageWithMeta(db *sql.DB, sessionID string, m provider.ChatMessage, turn int, meta DisplayMeta) error {
	defer sessionWrites.Since("append message", time.Now())
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = appendPersistedMessageTx(tx, sessionID, persistedMessage{Message: m, Meta: meta, Turn: turn, CreatedAt: time.Now().UnixNano()}); err != nil {
		return err
	}
	return tx.Commit()
}

func appendMessageWithMetaTx(tx *sql.Tx, sessionID string, delta ContextMessageDelta) error {
	return appendPersistedMessageTx(tx, sessionID, persistedMessage(delta))
}

func appendPersistedMessageTx(tx *sql.Tx, sessionID string, value persistedMessage) error {
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence_num),-1)+1 FROM messages`).Scan(&seq); err != nil {
		return err
	}
	m := value.Message
	var toolCallsJSON, cacheJSON, providerJSON, collaborationJSON []byte
	var err error
	if len(m.ToolCalls) != 0 {
		toolCallsJSON, err = json.Marshal(m.ToolCalls)
		if err != nil {
			return err
		}
	}
	if m.CacheControl != nil {
		cacheJSON, err = json.Marshal(m.CacheControl)
		if err != nil {
			return err
		}
	}
	if len(m.ProviderItems) != 0 {
		providerJSON, err = json.Marshal(m.ProviderItems)
		if err != nil {
			return err
		}
	}
	if m.Collaboration != nil {
		collaborationJSON, err = json.Marshal(m.Collaboration)
		if err != nil {
			return err
		}
	}
	content, toolResult := m.Content.String(), ""
	if m.Role == "tool" {
		toolResult, content = content, ""
	}
	contentType := "text"
	if !m.Content.IsText() {
		content, contentType = m.Content.MultipartJSON(), "multipart"
	}
	metaJSON, err := json.Marshal(value.Meta)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO messages(role,origin,visibility,internal_id,collaboration_json,content,content_type,reasoning_content,reasoning,tool_calls,tool_name,tool_result,tool_call_id,tool_error,provider_items,cache_control,token_count,sequence_num,turn_number,agent_id,created_at,display_meta) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.Role, m.EffectiveOrigin(), m.EffectiveVisibility(), m.InternalID, nullBytes(collaborationJSON), nullString(content), contentType, nullString(m.ReasoningContent), nullString(m.Reasoning), nullBytes(toolCallsJSON), nil, nullString(toolResult), nullString(m.ToolCallID), m.ToolError, nullBytes(providerJSON), nullBytes(cacheJSON), provider.EstimateMessageTokens(m, ""), seq, value.Turn, nullString(value.AgentID), value.CreatedAt, nullBytes(metaJSON))
	if err != nil {
		return err
	}
	if m.IsGenuineUserInput() {
		prompt := value.Meta.DisplayContent
		if prompt == "" {
			prompt = m.ContentString()
		}
		if len(prompt) > 200 {
			prompt = prompt[:200]
			for !utf8.ValidString(prompt) {
				prompt = prompt[:len(prompt)-1]
			}
		}
		if _, err := tx.Exec(`UPDATE sessions SET first_prompt=COALESCE(first_prompt,?),last_prompt=? WHERE id=?`, prompt, prompt, sessionID); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`UPDATE sessions SET message_count=(SELECT COUNT(*) FROM messages),turn_count=MAX(turn_count,?) WHERE id=?`, value.Turn, sessionID)
	if err != nil {
		return err
	}
	if _, err := result.RowsAffected(); err != nil {
		return err
	}
	return nil
}

func loadPersistedMessageByInternalIDTx(tx *sql.Tx, internalID string) (persistedMessage, bool, error) {
	rows, err := tx.Query(`SELECT role,origin,visibility,internal_id,collaboration_json,content,content_type,reasoning_content,reasoning,tool_calls,tool_name,tool_result,tool_call_id,tool_error,provider_items,cache_control,token_count,sequence_num,turn_number,agent_id,created_at,display_meta FROM messages WHERE internal_id=? ORDER BY sequence_num LIMIT 2`, internalID)
	if err != nil {
		return persistedMessage{}, false, err
	}
	defer rows.Close()
	var values []persistedMessage
	for rows.Next() {
		var row MessageRow
		if err := rows.Scan(&row.Role, &row.Origin, &row.Visibility, &row.InternalID, &row.Collaboration, &row.Content, &row.ContentType, &row.ReasoningContent, &row.Reasoning, &row.ToolCalls, &row.ToolName, &row.ToolResult, &row.ToolCallID, &row.ToolError, &row.ProviderItems, &row.CacheControl, &row.TokenCount, &row.SequenceNum, &row.TurnNumber, &row.AgentID, &row.CreatedAt, &row.DisplayMeta); err != nil {
			return persistedMessage{}, false, err
		}
		value, err := decodePersistedMessage(row)
		if err != nil {
			return persistedMessage{}, false, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return persistedMessage{}, false, err
	}
	if len(values) == 0 {
		return persistedMessage{}, false, nil
	}
	if len(values) != 1 {
		return persistedMessage{}, false, fmt.Errorf("sessiondb: duplicate immutable transcript id %q", internalID)
	}
	return values[0], true, nil
}

func decodePersistedMessage(r MessageRow) (persistedMessage, error) {
	m := provider.ChatMessage{InternalID: r.InternalID.String, Role: r.Role, Origin: provider.MessageOrigin(r.Origin), Visibility: provider.MessageVisibility(r.Visibility)}
	if r.Collaboration.Valid && r.Collaboration.String != "" {
		var value provider.CollaborationAttribution
		if err := json.Unmarshal([]byte(r.Collaboration.String), &value); err != nil {
			return persistedMessage{}, err
		}
		m.Collaboration = &value
	}
	m.Content = parseMessageContent(r.Content.String, r.ContentType.String)
	m.ReasoningContent, m.Reasoning, m.ToolCallID = r.ReasoningContent.String, r.Reasoning.String, r.ToolCallID.String
	if r.ToolCalls.Valid && r.ToolCalls.String != "" {
		if err := json.Unmarshal([]byte(r.ToolCalls.String), &m.ToolCalls); err != nil {
			return persistedMessage{}, err
		}
	}
	m.ToolError = r.ToolError != 0
	if r.ProviderItems.Valid && r.ProviderItems.String != "" {
		if err := json.Unmarshal([]byte(r.ProviderItems.String), &m.ProviderItems); err != nil {
			return persistedMessage{}, err
		}
	}
	if r.CacheControl.Valid && r.CacheControl.String != "" {
		var value provider.CacheControl
		if err := json.Unmarshal([]byte(r.CacheControl.String), &value); err != nil {
			return persistedMessage{}, err
		}
		m.CacheControl = &value
	}
	if m.Role == "tool" && r.ToolResult.Valid && r.ContentType.String != "multipart" {
		m.Content = provider.TextContent(r.ToolResult.String)
	}
	var meta DisplayMeta
	if r.DisplayMeta.Valid && r.DisplayMeta.String != "" {
		if err := json.Unmarshal([]byte(r.DisplayMeta.String), &meta); err != nil {
			return persistedMessage{}, err
		}
	}
	return persistedMessage{Message: m, Meta: meta, Turn: r.TurnNumber, CreatedAt: r.CreatedAt, AgentID: r.AgentID.String}, nil
}

func persistedMessageEqual(a, b persistedMessage) bool { return reflect.DeepEqual(a, b) }

// transcriptPersistedProjection limits transcript equality to the fields the
// messages table owns. Full immutable ChatMessage identity is verified by
// immutable_message_identities before this projection is compared.
func transcriptPersistedProjection(value persistedMessage) persistedMessage {
	m := value.Message
	m.DisplayContent = ""
	m.ReferenceLabels = nil
	m.Attachments = nil
	m.Refusal = ""
	m.ReasoningDetails = nil
	m.ReasoningDuration = 0
	m.ReasoningDisplay = reasoning.Display{}
	m.ReplayScope = ""
	m.ReplaySourceModel = ""
	m.ReplayPrefixHash = ""
	m.ResponseReference = nil
	// Empty optional slices are stored as SQL NULL. Their exact nil shape is
	// guarded by the full immutable identity, not the transcript projection.
	if len(m.ToolCalls) == 0 {
		m.ToolCalls = nil
	}
	if len(m.ProviderItems) == 0 {
		m.ProviderItems = nil
	}
	if m.Collaboration != nil {
		attribution := *m.Collaboration
		if len(attribution.RecipientIDs) == 0 {
			attribution.RecipientIDs = nil
		}
		if len(attribution.RecipientNames) == 0 {
			attribution.RecipientNames = nil
		}
		m.Collaboration = &attribution
	}
	value.Message = m
	return value
}

func canonicalPersistedMessage(value persistedMessage) persistedMessage {
	value.Message.Origin = value.Message.EffectiveOrigin()
	value.Message.Visibility = value.Message.EffectiveVisibility()
	return value
}

func LoadMessages(db *sql.DB) ([]provider.ChatMessage, []int, error) {
	rows, err := db.Query(`
		SELECT role, origin, visibility, internal_id, collaboration_json, content, content_type, reasoning_content, reasoning,
		       tool_calls, tool_name, tool_result, tool_call_id,
		       tool_error, provider_items, cache_control, token_count, sequence_num, turn_number,
		       agent_id, created_at, display_meta
		FROM messages ORDER BY sequence_num
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("sessiondb: load messages: %w", err)
	}
	defer rows.Close()

	var msgs []provider.ChatMessage
	var turns []int
	for rows.Next() {
		var r MessageRow
		err := rows.Scan(
			&r.Role, &r.Origin, &r.Visibility, &r.InternalID, &r.Collaboration,
			&r.Content, &r.ContentType, &r.ReasoningContent, &r.Reasoning,
			&r.ToolCalls, &r.ToolName, &r.ToolResult, &r.ToolCallID,
			&r.ToolError, &r.ProviderItems, &r.CacheControl, &r.TokenCount, &r.SequenceNum, &r.TurnNumber,
			&r.AgentID, &r.CreatedAt, &r.DisplayMeta,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("sessiondb: load messages scan: %w", err)
		}

		m := provider.ChatMessage{
			InternalID: r.InternalID.String,
			Role:       r.Role,
			Origin:     provider.MessageOrigin(r.Origin),
			Visibility: provider.MessageVisibility(r.Visibility),
		}
		if r.Collaboration.Valid && r.Collaboration.String != "" {
			var attribution provider.CollaborationAttribution
			if json.Unmarshal([]byte(r.Collaboration.String), &attribution) == nil {
				m.Collaboration = &attribution
			}
		}
		m.Content = parseMessageContent(r.Content.String, r.ContentType.String)

		if r.ReasoningContent.Valid {
			m.ReasoningContent = r.ReasoningContent.String
		}
		if r.Reasoning.Valid {
			m.Reasoning = r.Reasoning.String
		}
		if r.ToolCallID.Valid {
			m.ToolCallID = r.ToolCallID.String
		}
		if r.ToolCalls.Valid && r.ToolCalls.String != "" {
			_ = json.Unmarshal([]byte(r.ToolCalls.String), &m.ToolCalls)
		}
		m.ToolError = r.ToolError != 0
		if r.ProviderItems.Valid && r.ProviderItems.String != "" {
			_ = json.Unmarshal([]byte(r.ProviderItems.String), &m.ProviderItems)
		}
		if r.CacheControl.Valid && r.CacheControl.String != "" {
			var cc provider.CacheControl
			if json.Unmarshal([]byte(r.CacheControl.String), &cc) == nil {
				m.CacheControl = &cc
			}
		}
		if m.Role == "tool" && r.ToolResult.Valid && r.ContentType.String != "multipart" {
			m.Content = provider.TextContent(r.ToolResult.String)
		}

		msgs = append(msgs, m)
		turns = append(turns, r.TurnNumber)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("sessiondb: load messages rows: %w", err)
	}

	return msgs, turns, nil
}

func LoadMessagesWithMeta(db *sql.DB) ([]MessageWithMeta, error) {
	rows, err := db.Query(`
		SELECT role, origin, visibility, internal_id, collaboration_json, content, content_type, reasoning_content, reasoning,
		       tool_calls, tool_name, tool_result, tool_call_id,
		       tool_error, provider_items, cache_control, token_count, sequence_num, turn_number,
		       agent_id, created_at, display_meta
		FROM messages ORDER BY sequence_num
	`)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load messages with meta: %w", err)
	}
	defer rows.Close()

	var result []MessageWithMeta
	for rows.Next() {
		var r MessageRow
		err := rows.Scan(
			&r.Role, &r.Origin, &r.Visibility, &r.InternalID, &r.Collaboration,
			&r.Content, &r.ContentType, &r.ReasoningContent, &r.Reasoning,
			&r.ToolCalls, &r.ToolName, &r.ToolResult, &r.ToolCallID,
			&r.ToolError, &r.ProviderItems, &r.CacheControl, &r.TokenCount, &r.SequenceNum, &r.TurnNumber,
			&r.AgentID, &r.CreatedAt, &r.DisplayMeta,
		)
		if err != nil {
			return nil, fmt.Errorf("sessiondb: load messages with meta scan: %w", err)
		}

		m := provider.ChatMessage{
			InternalID: r.InternalID.String,
			Role:       r.Role,
			Origin:     provider.MessageOrigin(r.Origin),
			Visibility: provider.MessageVisibility(r.Visibility),
		}
		if r.Collaboration.Valid && r.Collaboration.String != "" {
			var attribution provider.CollaborationAttribution
			if json.Unmarshal([]byte(r.Collaboration.String), &attribution) == nil {
				m.Collaboration = &attribution
			}
		}
		m.Content = parseMessageContent(r.Content.String, r.ContentType.String)

		if r.ReasoningContent.Valid {
			m.ReasoningContent = r.ReasoningContent.String
		}
		if r.Reasoning.Valid {
			m.Reasoning = r.Reasoning.String
		}
		if r.ToolCallID.Valid {
			m.ToolCallID = r.ToolCallID.String
		}
		if r.ToolCalls.Valid && r.ToolCalls.String != "" {
			_ = json.Unmarshal([]byte(r.ToolCalls.String), &m.ToolCalls)
		}
		m.ToolError = r.ToolError != 0
		if r.ProviderItems.Valid && r.ProviderItems.String != "" {
			_ = json.Unmarshal([]byte(r.ProviderItems.String), &m.ProviderItems)
		}
		if r.CacheControl.Valid && r.CacheControl.String != "" {
			var cc provider.CacheControl
			if json.Unmarshal([]byte(r.CacheControl.String), &cc) == nil {
				m.CacheControl = &cc
			}
		}
		if m.Role == "tool" && r.ToolResult.Valid && r.ContentType.String != "multipart" {
			m.Content = provider.TextContent(r.ToolResult.String)
		}

		var meta DisplayMeta
		if r.DisplayMeta.Valid && r.DisplayMeta.String != "" {
			if err := json.Unmarshal([]byte(r.DisplayMeta.String), &meta); err != nil {
				meta = DisplayMeta{}
			}
		}
		m.DisplayContent = meta.DisplayContent
		m.ReferenceLabels = append([]string(nil), meta.ReferenceLabels...)
		m.Attachments = append([]attachment.Target(nil), meta.Attachments...)

		turn := r.TurnNumber
		if meta.DisplayRole == "" {
			meta.DisplayRole = r.Role
		}

		result = append(result, MessageWithMeta{
			Msg:       m,
			Meta:      meta,
			Turn:      turn,
			CreatedAt: r.CreatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessiondb: load messages with meta rows: %w", err)
	}

	return result, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullBytes(b []byte) sql.NullString {
	if len(b) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func parseMessageContent(s string, contentType string) provider.MessageContent {
	if contentType == "multipart" {
		var parts []provider.ContentPart
		if err := json.Unmarshal([]byte(s), &parts); err == nil && len(parts) > 0 {
			return provider.PartsContent(parts)
		}
		// Fallback: treat as text if JSON parse fails
	}
	// Default: treat as text content (covers both "text" and empty/legacy contentType)
	return provider.TextContent(s)
}
