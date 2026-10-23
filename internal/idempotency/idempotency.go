// Package idempotency defines the storage contract used by the Idempotency-Key
// middleware. Implementations live in the service layer; tests can supply an
// in-memory fake.
package idempotency

import "context"

// Record is a stored idempotent request/response pair.
type Record struct {
	RequestHash    string
	Status         string // "in_progress" or "completed"
	ResponseStatus int
	ResponseBody   []byte
}

// Store persists idempotency records scoped to a workspace and endpoint.
type Store interface {
	// Get returns the record for a key, if it exists.
	Get(ctx context.Context, workspaceID, endpoint, key string) (Record, bool, error)

	// Begin inserts an in-progress record. It returns false when another
	// request already created the same key.
	Begin(ctx context.Context, workspaceID, endpoint, key, requestHash string) (bool, error)

	// Complete stores the response and marks the record completed.
	Complete(ctx context.Context, workspaceID, endpoint, key string, status int, body []byte) error

	// Delete removes a record so a failed request can be retried.
	Delete(ctx context.Context, workspaceID, endpoint, key string) error
}
