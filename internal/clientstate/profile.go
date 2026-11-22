package clientstate

import (
	"context"
	"database/sql"
	"errors"
)

// ProfileState is observation only. An epoch never grants takeover authority;
// the application must independently exclude a live owner before requesting it.
type ProfileState struct {
	Epoch    uint64
	Attached bool
}

func (s *Store) Profile(ctx context.Context, client string) (ProfileState, error) {
	if !validID(client) {
		return ProfileState{}, ErrInvalid
	}
	var state ProfileState
	err := s.db.QueryRowContext(ctx, "SELECT epoch, attached FROM client_profiles WHERE client_id = ?", client).Scan(&state.Epoch, &state.Attached)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileState{}, ErrNotFound
	}
	return state, err
}
