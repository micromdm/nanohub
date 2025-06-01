// Package enqueue enqueues MDM commands to enrollments.
package enqueue

import (
	"context"
	"fmt"

	"github.com/jessepeterson/kmfddm/notifier"
	"github.com/micromdm/nanocmd/utils/uuid"
	"github.com/micromdm/nanomdm/api"
)

type RawCommandEnqueuer interface {
	// RawCommandEnqueueWithPush enqueues MDM commands and can send APNs pushes.
	RawCommandEnqueueWithPush(ctx context.Context, rawCommand []byte, ids []string, noPush bool) (*api.APIResult, int, error)
}

type IDer interface {
	// ID generates a unique identifier.
	// Ostensibly a UUID.
	ID() string
}

// Enqueue enqueues MDM commands to enrollments.
type Enqueue struct {
	ce     RawCommandEnqueuer
	ider   IDer
	noPush bool
}

// New creates a new enqueuer.
func New(ce RawCommandEnqueuer) *Enqueue {
	return &Enqueue{
		ce:   ce,
		ider: uuid.NewUUID(),
	}
}

// EnqueueDMCommand enqueues a Declarative Management MDM command.
// Optionally includes tokensJSON in the command.
func (e *Enqueue) EnqueueDMCommand(ctx context.Context, ids []string, tokensJSON []byte) error {
	cmdBytes, err := notifier.MakeCommand(e.ider.ID(), tokensJSON)
	if err != nil {
		return fmt.Errorf("making command: %w", err)
	}

	return e.Enqueue(ctx, ids, cmdBytes)
}

// Enqueue enqueues rawCmd to enrollment ids and sends an APNs push.
func (e *Enqueue) Enqueue(ctx context.Context, ids []string, rawCmd []byte) error {
	r, _, err := e.ce.RawCommandEnqueueWithPush(ctx, rawCmd, ids, e.noPush)
	if err != nil {
		return fmt.Errorf("raw push enqueue: %w", err)
	}

	return r.Error()
}

// SupportsMultiCommands returns true as NanoMDM natively supports multi-commands.
func (e *Enqueue) SupportsMultiCommands() bool {
	return true
}

// Push sends APNs pushes.
func (e *Enqueue) Push(ctx context.Context, ids []string) error {
	if e.noPush {
		return nil
	}

	return e.Enqueue(ctx, ids, nil)
}
