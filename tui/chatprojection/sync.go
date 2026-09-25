// Package chatprojection holds workspace→TUI chat message projection and rendering.
package chatprojection

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"crude/tui/types"
)

// MessageSync projects workspace-owned chat rows into a retained TUI store.
// Stable IDs preserve render caches; structural diffs drive virtlist updates.
type MessageSync struct {
	Messages      []*types.ChatMessage
	MsgByID       map[string]*types.ChatMessage
	version       uint64
	sourceVersion uint64
	msgIDSeq      uint64
}

// NewMessageSync creates an empty projection store.
func NewMessageSync() *MessageSync {
	return &MessageSync{MsgByID: make(map[string]*types.ChatMessage)}
}

// BumpVersion increments the render-generation counter.
func (s *MessageSync) BumpVersion() { s.version++ }

// Version returns the current render-generation counter.
func (s *MessageSync) Version() uint64 { return s.version }

// SourceVersion returns the last applied workspace conversation version.
func (s *MessageSync) SourceVersion() uint64 { return s.sourceVersion }

// SyncResult describes how a sync changed the projection.
type SyncResult struct {
	ChangedIDs       []string
	StructureChanged bool
	PureAppend       bool
	PreviousLen      int
}

// SyncMessages updates the retained projection from workspace-owned data.
// Returns a SyncResult when sourceVersion differs from the stored version.
// Caller applies virtlist AppendItem / InvalidateItem / RebuildItems.
func (s *MessageSync) SyncMessages(messages []types.ChatMessage, sourceVersion uint64) (SyncResult, bool) {
	if sourceVersion == s.sourceVersion {
		return SyncResult{}, false
	}

	previous := s.MsgByID
	previousMessages := s.Messages
	previousLen := len(previousMessages)
	next := make([]*types.ChatMessage, 0, len(messages))
	nextByID := make(map[string]*types.ChatMessage, len(messages)*2)
	changedIDs := make([]string, 0)
	structureChanged := len(messages) < previousLen
	pureAppend := len(messages) >= previousLen

	for index := range messages {
		incoming := messages[index]
		if incoming.InternalID == "" {
			incoming.InternalID = s.NextMsgID()
		}
		s.trackMessageID(incoming.InternalID)

		message := previous[incoming.InternalID]
		if message == nil {
			copy := incoming
			message = &copy
			structureChanged = true
			changedIDs = append(changedIDs, message.InternalID)
		} else if !sameMessageData(message, &incoming) {
			cachedLines := message.CachedLines
			cursorLine := message.CursorLine
			streamLines := message.StreamCompletedLines
			streamLen := message.StreamCompletedLen
			*message = incoming
			message.CachedLines = cachedLines
			message.CursorLine = cursorLine
			message.StreamCompletedLines = streamLines
			message.StreamCompletedLen = streamLen
			message.CachedLines = nil
			if strings.Contains(message.Content, "\n") {
				message.StreamCompletedLines = nil
				message.StreamCompletedLen = 0
			}
			changedIDs = append(changedIDs, message.InternalID)
		}
		if index < previousLen && previousMessages[index].InternalID != message.InternalID {
			structureChanged = true
			pureAppend = false
		}
		next = append(next, message)
		nextByID[message.InternalID] = message
		if message.ToolID != "" {
			nextByID[message.ToolID] = message
		}
	}

	s.Messages = next
	s.MsgByID = nextByID
	s.sourceVersion = sourceVersion
	s.BumpVersion()

	return SyncResult{
		ChangedIDs:       changedIDs,
		StructureChanged: structureChanged,
		PureAppend:       pureAppend,
		PreviousLen:      previousLen,
	}, true
}

// NextMsgID allocates a new stable message identifier.
func (s *MessageSync) NextMsgID() string {
	s.msgIDSeq++
	return fmt.Sprintf("msg-%d", s.msgIDSeq)
}

func (s *MessageSync) trackMessageID(id string) {
	if !strings.HasPrefix(id, "msg-") {
		return
	}
	value, err := strconv.ParseUint(strings.TrimPrefix(id, "msg-"), 10, 64)
	if err == nil && value > s.msgIDSeq {
		s.msgIDSeq = value
	}
}

func sameMessageData(current, incoming *types.ChatMessage) bool {
	return current.InternalID == incoming.InternalID &&
		current.Role == incoming.Role &&
		current.Content == incoming.Content &&
		slices.Equal(current.ReferenceLabels, incoming.ReferenceLabels) &&
		current.Time.Equal(incoming.Time) &&
		current.TurnGeneration == incoming.TurnGeneration &&
		current.ProviderID == incoming.ProviderID &&
		current.ModelID == incoming.ModelID &&
		current.ToolName == incoming.ToolName &&
		current.ToolArgs == incoming.ToolArgs &&
		current.ToolCommand == incoming.ToolCommand &&
		current.ToolInput == incoming.ToolInput &&
		current.ToolScope == incoming.ToolScope &&
		current.ToolID == incoming.ToolID &&
		current.ToolRoundID == incoming.ToolRoundID &&
		current.ToolStatus == incoming.ToolStatus &&
		current.ToolIndex == incoming.ToolIndex &&
		current.ToolProgressPhase == incoming.ToolProgressPhase &&
		current.ToolProgressMessage == incoming.ToolProgressMessage &&
		current.ToolProgressLabel == incoming.ToolProgressLabel &&
		current.ToolPreviewPhase == incoming.ToolPreviewPhase &&
		current.ToolEstTokens == incoming.ToolEstTokens &&
		current.ToolLivedAdded == incoming.ToolLivedAdded &&
		current.ToolLivedRemoved == incoming.ToolLivedRemoved &&
		current.ToolStartTime.Equal(incoming.ToolStartTime) &&
		current.ToolDuration == incoming.ToolDuration &&
		current.ThinkingActive == incoming.ThinkingActive &&
		current.ThinkingInterrupted == incoming.ThinkingInterrupted &&
		current.ThinkingDuration == incoming.ThinkingDuration &&
		current.ThinkingDisplay == incoming.ThinkingDisplay &&
		current.LifecyclePhase == incoming.LifecyclePhase &&
		current.LifecycleProgress == incoming.LifecycleProgress &&
		current.LifecycleProvider == incoming.LifecycleProvider &&
		current.LifecycleUploadedBytes == incoming.LifecycleUploadedBytes &&
		current.LifecycleTotalBytes == incoming.LifecycleTotalBytes &&
		current.LifecycleBeforeTokens == incoming.LifecycleBeforeTokens &&
		current.LifecycleAfterTokens == incoming.LifecycleAfterTokens &&
		current.LifecycleOperationID == incoming.LifecycleOperationID &&
		current.LifecycleTrigger == incoming.LifecycleTrigger &&
		current.LifecycleStageName == incoming.LifecycleStageName &&
		current.LifecycleStageIndex == incoming.LifecycleStageIndex &&
		current.LifecycleCompleted == incoming.LifecycleCompleted &&
		current.LifecycleStageCount == incoming.LifecycleStageCount &&
		current.LifecycleRetryAttempt == incoming.LifecycleRetryAttempt &&
		current.LifecycleRetryAt.Equal(incoming.LifecycleRetryAt) &&
		current.LifecycleRetryLimit == incoming.LifecycleRetryLimit &&
		current.LifecycleContextWindow == incoming.LifecycleContextWindow &&
		current.PendingBtw == incoming.PendingBtw &&
		current.Incomplete == incoming.Incomplete &&
		current.IncompleteReason == incoming.IncompleteReason &&
		current.SubPaneID == incoming.SubPaneID &&
		current.PermTag == incoming.PermTag
}
