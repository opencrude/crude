package session

import (
	"context"
	"database/sql"
	"fmt"

	"crude/sessiondb"
	"crude/statemachine"
)

const conversationStateKey = "conversation_state"

func persistConversationState(
	ctx context.Context,
	db *sql.DB,
	machine *statemachine.ConversationStateMachine,
	conversationID string,
) error {
	if db == nil || machine == nil || conversationID == "" {
		return nil
	}
	data, err := machine.Snapshot(conversationID)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return sessiondb.SaveRuntimeState(ctx, db, conversationStateKey, data)
}

func restoreConversationState(
	ctx context.Context,
	db *sql.DB,
	machine *statemachine.ConversationStateMachine,
	conversationID string,
) error {
	data, err := sessiondb.LoadRuntimeState(ctx, db, conversationStateKey)
	if err != nil {
		return err
	}
	if err := machine.Restore(conversationID, data); err != nil {
		return fmt.Errorf("restore conversation state: %w", err)
	}
	return nil
}
