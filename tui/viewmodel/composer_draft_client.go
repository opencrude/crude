package viewmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"

	"crude/clientstate"
)

// The binding is immutable. Commands retain it rather than consulting mutable
// App state after a workspace/profile change. The caller owns Store lifetime.
type composerDraftClient struct {
	store       *clientstate.Store
	attachment  clientstate.Attachment
	workspaceID string
}

type composerDraftRecord struct {
	mu       sync.Mutex
	key      clientstate.Key
	known    bool
	revision uint64
	pending  *clientstate.Put
}

func (c *composerDraftClient) record(sessionID string) *composerDraftRecord {
	return &composerDraftRecord{key: clientstate.Key{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, SessionID: sessionID}}
}

// ConfigureComposerDrafts installs an explicitly acquired private profile. It
// never reads or converts session runtime state. Call off Update: rebinding is
// a teardown barrier and can perform bounded storage I/O.
func (m *App) ConfigureComposerDrafts(store *clientstate.Store, attachment clientstate.Attachment, workspaceID string) error {
	if store == nil || attachment.ClientID == "" || attachment.Epoch == 0 || workspaceID == "" {
		return clientstate.ErrInvalid
	}
	m.flushComposerDraft()
	if m.composerDraftErr != nil {
		return m.composerDraftErr
	}
	m.composerDraftClient = &composerDraftClient{store: store, attachment: attachment, workspaceID: workspaceID}
	m.composerDraftOwners = nil
	m.composerHydratedFor = nil
	m.composerDraftEpoch++
	return nil
}

// ChangeComposerDraftWorkspace must precede destination session activation.
// A failed flush leaves the old binding intact; attachment release is the
// application owner's responsibility after its final StopAsyncIO barrier.
func (m *App) ChangeComposerDraftWorkspace(workspaceID string) error {
	c := m.composerDraftClient
	if c == nil {
		return nil
	}
	if workspaceID == c.workspaceID {
		return nil
	}
	return m.ConfigureComposerDrafts(c.store, c.attachment, workspaceID)
}

func (r *composerDraftRecord) read(c *composerDraftClient, ctx context.Context) (clientstate.Record, error) {
	record, err := c.store.LoadContext(ctx, c.attachment.ClientID, c.attachment.Epoch, r.key)
	if errors.Is(err, clientstate.ErrNotFound) {
		err = nil
	}
	if err == nil {
		r.known, r.revision = true, record.Revision
	}
	return record, err
}

func (r *composerDraftRecord) load(c *composerDraftClient) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	record, err := r.read(c, ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(record.Draft)
}

func (r *composerDraftRecord) save(c *composerDraftClient, raw []byte) error {
	if r == nil || c == nil {
		return clientstate.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	if !r.known {
		if _, err := r.read(c, ctx); err != nil {
			return err
		}
	}
	// Resolve an uncertain prior write with its original immutable request before
	// attempting newer text. Replaying the receipt cannot repeat the old effect.
	if r.pending != nil {
		record, err := c.store.Save(ctx, *r.pending)
		if err != nil {
			return err
		}
		prior, _ := json.Marshal(r.pending.Draft)
		r.revision, r.pending = record.Revision, nil
		if bytes.Equal(prior, raw) {
			return nil
		}
	}
	var draft clientstate.Draft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return err
	}
	r.pending = &clientstate.Put{ClientID: c.attachment.ClientID, Epoch: c.attachment.Epoch,
		RequestID: fmt.Sprintf("composer-%016x%016x", rand.Uint64(), rand.Uint64()),
		Key:       r.key, ExpectedRevision: r.revision, Draft: draft}
	record, err := c.store.Save(ctx, *r.pending)
	if err != nil {
		return err
	}
	r.revision, r.pending = record.Revision, nil
	return nil
}
