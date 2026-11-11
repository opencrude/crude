package types

import "errors"

// ErrIdleSettling distinguishes an occupied, transient admission from work that
// requires a user decision. A stop may wait briefly without cancelling the owner.
var ErrIdleSettling = errors.New("an operation is still finishing")

type idleSettling string

// IdleSettling preserves an owner's actionable refusal while marking it as
// transient. Consumers use errors.Is, never parse the message to decide safety.
func IdleSettling(message string) error { return idleSettling(message) }

func (e idleSettling) Error() string        { return string(e) }
func (e idleSettling) Is(target error) bool { return target == ErrIdleSettling }
