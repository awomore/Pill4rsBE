package service

import (
	"context"
	"errors"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// IdempotencyStore is the Postgres-backed implementation of
// idempotency.Store.
type IdempotencyStore struct {
	queries *db.Queries
}

func NewIdempotencyStore(queries *db.Queries) *IdempotencyStore {
	return &IdempotencyStore{queries: queries}
}

func (s *IdempotencyStore) Get(ctx context.Context, workspaceID, endpoint, key string) (idempotency.Record, bool, error) {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return idempotency.Record{}, false, err
	}
	row, err := s.queries.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{
		WorkspaceID: pgtype.UUID{Bytes: wid, Valid: true},
		Endpoint:    endpoint,
		Key:         key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Record{}, false, nil
		}
		return idempotency.Record{}, false, err
	}
	rec := idempotency.Record{
		RequestHash:  row.RequestHash,
		Status:       row.Status,
		ResponseBody: row.ResponseBody,
	}
	if row.ResponseStatus.Valid {
		rec.ResponseStatus = int(row.ResponseStatus.Int32)
	}
	return rec, true, nil
}

func (s *IdempotencyStore) Begin(ctx context.Context, workspaceID, endpoint, key, requestHash string) (bool, error) {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return false, err
	}
	rows, err := s.queries.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
		WorkspaceID: pgtype.UUID{Bytes: wid, Valid: true},
		Endpoint:    endpoint,
		Key:         key,
		RequestHash: requestHash,
	})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *IdempotencyStore) Complete(ctx context.Context, workspaceID, endpoint, key string, status int, body []byte) error {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return err
	}
	return s.queries.CompleteIdempotencyKey(ctx, db.CompleteIdempotencyKeyParams{
		WorkspaceID:    pgtype.UUID{Bytes: wid, Valid: true},
		Endpoint:       endpoint,
		Key:            key,
		ResponseStatus: pgtype.Int4{Int32: int32(status), Valid: true},
		ResponseBody:   body,
	})
}

func (s *IdempotencyStore) Delete(ctx context.Context, workspaceID, endpoint, key string) error {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return err
	}
	return s.queries.DeleteIdempotencyKey(ctx, db.DeleteIdempotencyKeyParams{
		WorkspaceID: pgtype.UUID{Bytes: wid, Valid: true},
		Endpoint:    endpoint,
		Key:         key,
	})
}

// DeleteExpired removes records older than the given time.
func (s *IdempotencyStore) DeleteExpired(ctx context.Context, before time.Time) error {
	return s.queries.DeleteExpiredIdempotencyKeys(ctx, pgtype.Timestamptz{Time: before, Valid: true})
}
