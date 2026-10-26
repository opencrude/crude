package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// These are metadata copies at existing publication boundaries, never another
// cursor, replication owner, or reader of live document/terminal encoder state.
type topicObservation struct {
	WorkspaceID       string    `json:"workspace_id,omitempty"`
	Topic             string    `json:"topic"`
	ResourceID        string    `json:"resource_id,omitempty"`
	Generation        uint64    `json:"generation"`
	Active            bool      `json:"active"`
	Sent              uint64    `json:"sent_sequence"`
	Ack               uint64    `json:"acked_sequence"`
	SentAt            time.Time `json:"sent_at,omitempty"`
	AckAt             time.Time `json:"acked_at,omitempty"`
	PendingSince      time.Time `json:"pending_since,omitempty"`
	PendingAgeMS      int64     `json:"pending_ack_age_ms"`
	Resets            uint64    `json:"resets"`
	ResetReason       string    `json:"reset_reason,omitempty"`
	DisplayIntervalNS int64     `json:"display_interval_ns,omitempty"`
}

type topicObservationContext struct{}
type topicObservationToken struct {
	connection *Connection
	key        string
	value      *topicObservation
}

const diagnosticTopicLimit = 1024

func (c *Connection) claimTopicObservationLocked(ctx context.Context, key string) context.Context {
	if c.topicObservations == nil {
		c.topicObservations = make(map[string]*topicObservation)
	}
	if strings.Contains(key, "/input/") {
		return ctx
	} // input is not a replication subscription
	if c.topicObservations[key] == nil && len(c.topicObservations) >= diagnosticTopicLimit {
		for old, value := range c.topicObservations {
			if !value.Active {
				delete(c.topicObservations, old)
				break
			}
		}
		if len(c.topicObservations) >= diagnosticTopicLimit {
			c.topicObservationDropped++
			return ctx
		}
	}
	c.topicGeneration++
	value := &topicObservation{Generation: c.topicGeneration, Active: true}
	if prior := c.topicObservations[key]; prior != nil {
		*value = *prior
		value.Generation, value.Active = c.topicGeneration, true
	}
	parts := strings.SplitN(key, "/", 3)
	value.Topic = parts[0]
	if len(parts) > 1 {
		value.WorkspaceID, value.Topic = parts[0], parts[1]
	}
	if len(parts) > 2 {
		sum := sha256.Sum256([]byte(parts[2]))
		value.ResourceID = hex.EncodeToString(sum[:8])
	}
	c.topicObservations[key] = value
	return context.WithValue(ctx, topicObservationContext{}, topicObservationToken{c, key, value})
}

func updateTopicObservation(ctx context.Context, update func(*topicObservation)) {
	token, ok := ctx.Value(topicObservationContext{}).(topicObservationToken)
	if !ok {
		return
	}
	c := token.connection
	c.topicMu.Lock()
	defer c.topicMu.Unlock()
	if c.topicObservations[token.key] == token.value && token.value.Active {
		update(token.value)
	}
}

func observeTopicSent(ctx context.Context, seq uint64, resetReason string) {
	updateTopicObservation(ctx, func(value *topicObservation) {
		now := time.Now()
		if value.Sent != seq || value.PendingSince.IsZero() {
			value.PendingSince = now
		}
		value.Sent, value.SentAt = seq, now
		if resetReason != "" {
			value.Resets++
			value.ResetReason = resetReason
		}
	})
}

func observeTopicAck(ctx context.Context, seq uint64, display int64) {
	updateTopicObservation(ctx, func(value *topicObservation) {
		if value.Sent != seq {
			return
		}
		value.Ack, value.AckAt, value.PendingSince = seq, time.Now(), time.Time{}
		if display != 0 {
			value.DisplayIntervalNS = int64(clampDisplay(display))
		}
	})
}
