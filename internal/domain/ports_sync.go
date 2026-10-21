package domain

import (
	"context"
	"time"
)

// Sync read-model port (hexagonal slice 3). The offline-sync endpoint is a
// cursor-paged contract, so its full read surface is a single core-owned
// port; package repository implements it against the public catalog tables.
type SyncStore interface {
	EventsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Event], error)
	VenuesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Venue], error)
	OrganizersChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Organizer], error)
	CategoriesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Category], error)
	TicketTypesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*TicketType], error)
	CommentsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Comment], error)
	ReviewsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*Review], error)
	DeletedIDsChangedSince(ctx context.Context, since time.Time, domainName string, limit int) (SyncPage[DeletedChange], error)
}

// SyncPage is one domain page: up to `limit` rows to emit, the watermark the
// caller's next cursor will be advanced to (the max updated_at/change time over
// every row READ this page, including the limit+1th sentinel that only proves
// more rows exist), and HasMore. Using the full read horizon for the watermark
// is what lets a boundary row be re-emitted without stalling pagination.
type SyncPage[T any] struct {
	Rows      []T
	Watermark time.Time
	HasMore   bool
}

// DeletedChange is a soft-deleted row id plus the timestamp at which the row
// changed (the delete's updated_at bump), so the caller can fold it into its
// per-domain watermark.
type DeletedChange struct {
	ID        string
	ChangedAt time.Time
}
