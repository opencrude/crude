package session

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/types"
)

// Hygiene is a request-boundary reminder for the owning parent, not an admission
// budget, a background scan, or permission to delete another agent's work.
func (s *Session) executionHygieneNotice(ctx context.Context, settings types.ExecutionHygieneSettings, previous string) (*provider.ChatMessage, string) {
	enabled, countLimit, ageLimit, storageLimit := settings.Values()
	if !enabled || countLimit == 0 && ageLimit == 0 && storageLimit == 0 {
		return nil, ""
	}
	service, err := s.executionService()
	if err != nil || ctx.Err() != nil {
		return nil, previous
	}
	var report rootprompt.ExecutionHygiene
	var fingerprint strings.Builder
	now := time.Now()
	for _, c := range service.List() {
		if c.Removed || c.Kept {
			continue
		}
		report.Count++
		report.StorageBytes += c.StorageBytes
		report.OldestHours = max(report.OldestHours, int(now.Sub(c.CreatedAt).Hours()))
		if len(report.ExecutionIDs) < 8 {
			report.ExecutionIDs = append(report.ExecutionIDs, c.ID)
		}
		fingerprint.WriteString(c.ID + ":" + strconv.Itoa(len(c.Pins)) + ";")
	}
	if countLimit > 0 && report.Count >= countLimit {
		report.Reasons = append(report.Reasons, fmt.Sprintf("retained count %d >= %d", report.Count, countLimit))
	}
	if ageLimit > 0 && report.OldestHours >= ageLimit {
		report.Reasons = append(report.Reasons, fmt.Sprintf("oldest age %d hours >= %d", report.OldestHours, ageLimit))
	}
	if storageLimit > 0 && report.StorageBytes >= int64(storageLimit)<<20 {
		report.Reasons = append(report.Reasons, fmt.Sprintf("snapshot storage >= %d MB", storageLimit))
	}
	if len(report.Reasons) == 0 {
		return nil, ""
	}
	fingerprint.WriteString(fmt.Sprintf("%t:%d:%d:%d", enabled, countLimit, ageLimit, storageLimit) + strings.Join(report.Reasons, ";") + strconv.FormatInt(report.StorageBytes, 10))
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint.String())))
	if digest == previous {
		return nil, previous
	}
	notice := runtimeMessage(rootprompt.ExecutionHygieneNotice(report))
	return &notice, digest
}
