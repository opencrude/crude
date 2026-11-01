// Package chatprojection holds workspace→TUI chat message projection and rendering.
package chatprojection

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"crude/types"
)

// MessageSync projects workspace-owned chat rows into a retained TUI store.
// Stable IDs preserve render caches; structural diffs drive virtlist updates.
type MessageSync struct {
	Messages         []*types.ChatMessage
	MsgByID          map[string]*types.ChatMessage
	version          uint64
	sourceVersion    uint64
	sourceGeneration uint64
	sourceSet        bool
	msgIDSeq         uint64
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

// SourceGeneration fences revisions reused by a replacement source snapshot.
func (s *MessageSync) SourceGeneration() uint64 { return s.sourceGeneration }

// SyncResult describes how a sync changed the projection.
type SyncResult struct {
	ChangedIDs       []string
	StructureChanged bool
	PureAppend       bool
	PreviousLen      int
}

// SyncMessages updates the retained projection from workspace-owned data.
// Returns a SyncResult when the source generation/version tuple changes.
// Caller applies virtlist AppendItem / InvalidateItem / RebuildItems.
func (s *MessageSync) SyncMessages(messages []types.ChatMessage, sourceVersion, sourceGeneration uint64) (SyncResult, bool) {
	if s.sourceSet && sourceVersion == s.sourceVersion && sourceGeneration == s.sourceGeneration {
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
	s.sourceGeneration = sourceGeneration
	s.sourceSet = true
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
		current.Origin == incoming.Origin &&
		reflect.DeepEqual(current.Collaboration, incoming.Collaboration) &&
		current.Content == incoming.Content &&
		slices.Equal(current.ReferenceLabels, incoming.ReferenceLabels) &&
		slices.Equal(current.Attachments, incoming.Attachments) &&
		current.Time.Equal(incoming.Time) &&
		current.TurnGeneration == incoming.TurnGeneration &&
		current.ProviderID == incoming.ProviderID &&
		current.AccountID == incoming.AccountID &&
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
		current.HistoryPathCount == incoming.HistoryPathCount &&
		current.HistoryTargetSnapshot == incoming.HistoryTargetSnapshot &&
		current.HistorySafetySnapshot == incoming.HistorySafetySnapshot &&
		recoveryEqual(current.Recovery, incoming.Recovery) &&
		current.PendingSteering == incoming.PendingSteering &&
		current.Incomplete == incoming.Incomplete &&
		current.IncompleteReason == incoming.IncompleteReason &&
		current.SubPaneID == incoming.SubPaneID &&
		current.PermTag == incoming.PermTag
}

// ApplyChanges installs an exact source delta: changed rows (complete
// records), removed identities, and, when membership changed, the complete new
// order. Work is proportional to the change plus a pointer-prefix comparison,
// never to the bodies of unchanged rows. It returns false without mutating
// anything when the delta cannot be applied exactly; the caller then performs
// a complete SyncMessages.
func (s *MessageSync) ApplyChanges(changed []types.ChatMessage, removed, order []string, sourceVersion, sourceGeneration uint64) (SyncResult, bool) {
	if !s.sourceSet || sourceGeneration != s.sourceGeneration || sourceVersion <= s.sourceVersion {
		return SyncResult{}, false
	}
	incoming := make(map[string]int, len(changed))
	for i := range changed {
		id := changed[i].InternalID
		if id == "" {
			return SyncResult{}, false
		}
		if _, duplicate := incoming[id]; duplicate {
			return SyncResult{}, false
		}
		if existing := s.MsgByID[id]; existing == nil || existing.InternalID != id {
			if order == nil {
				return SyncResult{}, false // a new row without its position
			}
		}
		incoming[id] = i
	}
	if len(removed) != 0 && order == nil {
		return SyncResult{}, false
	}
	removedSet := make(map[string]bool, len(removed))
	for _, id := range removed {
		removedSet[id] = true
	}
	previousLen := len(s.Messages)
	prefix := previousLen
	if order != nil {
		prefix = min(previousLen, len(order))
		for i := range prefix {
			if s.Messages[i].InternalID != order[i] {
				prefix = i
				break
			}
		}
		seen := make(map[string]bool, len(order)-prefix)
		for _, id := range order[prefix:] {
			if seen[id] || removedSet[id] {
				return SyncResult{}, false
			}
			seen[id] = true
			if _, ok := incoming[id]; ok {
				continue
			}
			if existing := s.MsgByID[id]; existing == nil || existing.InternalID != id {
				return SyncResult{}, false
			}
		}
	}

	changedIDs := make([]string, 0, len(changed))
	for i := range changed {
		next := changed[i]
		s.trackMessageID(next.InternalID)
		message := s.MsgByID[next.InternalID]
		if message == nil || message.InternalID != next.InternalID {
			copy := next
			message = &copy
			s.MsgByID[message.InternalID] = message
		} else if !sameMessageData(message, &next) {
			oldToolID := message.ToolID
			cursorLine := message.CursorLine
			streamLines, streamLen := message.StreamCompletedLines, message.StreamCompletedLen
			*message = next
			message.CachedLines = nil
			message.CursorLine = cursorLine
			message.StreamCompletedLines, message.StreamCompletedLen = streamLines, streamLen
			if strings.Contains(message.Content, "\n") {
				message.StreamCompletedLines, message.StreamCompletedLen = nil, 0
			}
			if oldToolID != "" && oldToolID != message.ToolID && s.MsgByID[oldToolID] == message {
				delete(s.MsgByID, oldToolID)
			}
		} else {
			continue
		}
		if message.ToolID != "" {
			s.MsgByID[message.ToolID] = message
		}
		changedIDs = append(changedIDs, message.InternalID)
	}
	for _, id := range removed {
		message := s.MsgByID[id]
		if message == nil || message.InternalID != id {
			continue
		}
		delete(s.MsgByID, id)
		if message.ToolID != "" && s.MsgByID[message.ToolID] == message {
			delete(s.MsgByID, message.ToolID)
		}
	}
	structureChanged := false
	pureAppend := true
	if order != nil {
		members := make(map[string]bool, len(order)-prefix)
		for _, id := range order[prefix:] {
			members[id] = true
		}
		for _, message := range s.Messages[prefix:] {
			if !members[message.InternalID] && s.MsgByID[message.InternalID] == message {
				delete(s.MsgByID, message.InternalID)
				if message.ToolID != "" && s.MsgByID[message.ToolID] == message {
					delete(s.MsgByID, message.ToolID)
				}
			}
		}
		next := append(s.Messages[:prefix:prefix], make([]*types.ChatMessage, 0, len(order)-prefix)...)
		for _, id := range order[prefix:] {
			next = append(next, s.MsgByID[id])
		}
		structureChanged = prefix < previousLen || len(order) != previousLen
		pureAppend = prefix == previousLen
		s.Messages = next
	}
	s.sourceVersion = sourceVersion
	s.BumpVersion()
	return SyncResult{ChangedIDs: changedIDs, StructureChanged: structureChanged, PureAppend: pureAppend, PreviousLen: previousLen}, true
}
