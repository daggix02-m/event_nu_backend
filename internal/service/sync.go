package service

import (
	"context"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/repository"
)

// Sync domains served by GET /api/v1/sync. This is the public-discovery
// catalog: content the client caches for offline reading. User-scoped rows
// (saves, rsvps, reminders, notifications, follows) are deliberately absent —
// the client refreshes those wholesale via their own endpoints on reconnect.
const (
	SyncDomainEvents      = "events"
	SyncDomainVenues      = "venues"
	SyncDomainOrganizers  = "organizers"
	SyncDomainCategories  = "categories"
	SyncDomainTicketTypes = "ticket_types"
	SyncDomainComments    = "comments"
	SyncDomainReviews     = "reviews"
)

const (
	changeUpsert = "upsert"
	changeDelete = "delete"
)

// SyncDomainsOrder is the canonical order changes are emitted in. Because
// soft deletes are only tracked (and whitelisted) for tables with a real
// deleted_at, deletes only apply to a subset; comment rows are hard-deleted
// and never emit delete ops.
var SyncDomainsOrder = []string{
	SyncDomainEvents,
	SyncDomainVenues,
	SyncDomainOrganizers,
	SyncDomainCategories,
	SyncDomainTicketTypes,
	SyncDomainComments,
	SyncDomainReviews,
}

// SyncWhitelist validates the ?domains= query param.
var SyncWhitelist = func() map[string]struct{} {
	m := make(map[string]struct{}, len(SyncDomainsOrder))
	for _, d := range SyncDomainsOrder {
		m[d] = struct{}{}
	}
	return m
}()

// SyncDeleteCapable marks domains whose soft deletes are surfaced as delete
// ops. Matches the sync_deleted_ids() whitelist in migration 00018.
var SyncDeleteCapable = map[string]bool{
	SyncDomainEvents:     true,
	SyncDomainVenues:     true,
	SyncDomainOrganizers: true,
	SyncDomainReviews:    true,
}

// Change is one delta entry: a row to upsert (Row set) or a soft-deleted row
// id to drop (Row nil). Public content in every payload — delete ops only
// carry the id.
type Change struct {
	Domain string
	Op     string
	ID     string
	Row    any
}

// PullOptions are the sync request parameters.
type PullOptions struct {
	Cursor  time.Time
	Domains []string
	Limit   int
}

// PullResult is the aggregated delta response.
type PullResult struct {
	Changes    []Change
	NextCursor time.Time
	HasMore    map[string]bool
}

type domainPull struct {
	Changes   []Change
	Watermark time.Time
	HasMore   bool
}

// SyncService aggregates per-domain deltas into one cursor-based response.
type SyncService struct {
	repo *repository.SyncRepository
}

func NewSyncService(repo *repository.SyncRepository) *SyncService {
	return &SyncService{repo: repo}
}

// Pull returns, for each requested domain, the rows changed after the cursor
// (upsert: currently visible; delete: soft-deleted since the cursor) plus a
// next cursor. The cursor is inclusive and per-domain pages are capped at
// limit. When any domain still has more rows, NextCursor is held at the
// smallest watermark among unfinished domains so the following poll resumes
// without gaps; otherwise it advances to the maximum watermark (further
// advances are then monotonic). Both cases may re-emit a page boundary row —
// consumers upsert idempotently.
func (s *SyncService) Pull(ctx context.Context, opts PullOptions) (*PullResult, error) {
	domains := opts.Domains
	if len(domains) == 0 {
		domains = SyncDomainsOrder
	}

	var (
		changes []Change
		hasMore = make(map[string]bool, len(domains))
		advance = opts.Cursor // max watermark once every domain is exhausted
		clamp   = opts.Cursor // min watermark among domains that still have rows
		clamped bool
		anyMore bool
	)

	for _, name := range domains {
		res, err := s.pullDomain(ctx, name, opts.Cursor, opts.Limit)
		if err != nil {
			return nil, err
		}
		hasMore[name] = res.HasMore
		anyMore = anyMore || res.HasMore
		changes = append(changes, res.Changes...)
		if res.Watermark.After(advance) {
			advance = res.Watermark
		}
		if res.HasMore && (!clamped || res.Watermark.Before(clamp)) {
			clamp = res.Watermark
			clamped = true
		}
	}

	next := opts.Cursor
	switch {
	case !anyMore:
		next = advance
	case clamped:
		next = clamp
	}

	return &PullResult{Changes: changes, NextCursor: next, HasMore: hasMore}, nil
}

func (s *SyncService) pullDomain(ctx context.Context, name string, since time.Time, limit int) (*domainPull, error) {
	res := &domainPull{Watermark: since}

	switch name {
	case SyncDomainEvents:
		page, err := s.repo.EventsChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: e.ID, Row: e})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainVenues:
		page, err := s.repo.VenuesChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: v.ID, Row: v})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainOrganizers:
		page, err := s.repo.OrganizersChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: o.ID, Row: o})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainCategories:
		page, err := s.repo.CategoriesChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: c.ID, Row: c})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainTicketTypes:
		page, err := s.repo.TicketTypesChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, tt := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: tt.ID, Row: tt})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainComments:
		page, err := s.repo.CommentsChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: c.ID, Row: c})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	case SyncDomainReviews:
		page, err := s.repo.ReviewsChangedSince(ctx, since, limit)
		if err != nil {
			return nil, err
		}
		for _, rev := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeUpsert, ID: rev.ID, Row: rev})
		}
		res.Watermark = page.Watermark
		res.HasMore = page.HasMore
	default:
		return nil, fmt.Errorf("unsupported sync domain: %s", name)
	}

	if SyncDeleteCapable[name] {
		page, err := s.repo.DeletedIDsChangedSince(ctx, since, name, limit)
		if err != nil {
			return nil, err
		}
		for _, d := range page.Rows {
			res.Changes = append(res.Changes, Change{Domain: name, Op: changeDelete, ID: d.ID})
		}
		if page.Watermark.After(res.Watermark) {
			res.Watermark = page.Watermark
		}
		res.HasMore = page.HasMore || res.HasMore
	}

	return res, nil
}
