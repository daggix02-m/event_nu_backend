package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
	"github.com/daggix02-m/event_nu_backend/internal/validator"
)

// reportAdminStore is the moderation-report surface AdminService needs for its
// review queue. Queries run under the admin RLS role (is_privileged), so every
// row is visible.
type reportAdminStore interface {
	ListAll(ctx context.Context, limit, offset int) ([]*domain.Report, error)
	CountAll(ctx context.Context) (int, error)
	Resolve(ctx context.Context, id, resolution string) (*domain.Report, error)
}

// applicationAdminStore is the organizer-application surface the admin queue
// reads. Status is an optional filter; queries run under the admin RLS role.
type applicationAdminStore interface {
	ListApplications(ctx context.Context, status string, limit, offset int) ([]*domain.OrganizerApplication, error)
	CountApplications(ctx context.Context, status string) (int, error)
}

type adminEventsStore interface {
	AdminList(ctx context.Context, status, moderation string, limit, offset int) ([]*domain.Event, error)
	AdminCount(ctx context.Context, status, moderation string) (int, error)
	CountByOrganizer(ctx context.Context, organizerID string) (int, error)
	AdminListByOrganizer(ctx context.Context, organizerID string, limit, offset int) ([]*domain.Event, error)
	UpdateOrganizer(ctx context.Context, eventID, organizerID string) (*domain.Event, error)
}

// adminOrganizerStore is the organizer surface the console reads and writes:
// application-independent listing/detail plus direct creation. Queries run
// under the admin RLS role (is_privileged), so suspended/archived rows stay
// visible.
type adminOrganizerStore interface {
	GetOrganizerByID(ctx context.Context, id string) (*domain.Organizer, error)
	GetOrganizerBySlug(ctx context.Context, slug string) (*domain.Organizer, error)
	GetOrganizerByOwner(ctx context.Context, userID string) (*domain.Organizer, error)
	CreateOrganizer(ctx context.Context, o *domain.Organizer) (*domain.Organizer, error)
	ListAdmin(ctx context.Context, q, status string, limit, offset int) ([]*domain.Organizer, error)
	CountAdmin(ctx context.Context, q, status string) (int, error)
}

// adminUsersStore is the user-directory surface the console reads and edits.
// Queries run under the admin RLS role, so any role or status is visible.
type adminUsersStore interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
	ListAdmin(ctx context.Context, q, role, status string, limit, offset int) ([]*domain.User, error)
	CountAdmin(ctx context.Context, q, role, status string) (int, error)
	UpdateAdmin(ctx context.Context, id string, username, bio, photoURL, role, status *string) (*domain.User, error)
	SetStatus(ctx context.Context, id, status string) (*domain.User, error)
}

type adminVenueStore interface {
	Create(ctx context.Context, v *domain.Venue) (*domain.Venue, error)
	GetByID(ctx context.Context, id string) (*domain.Venue, error)
	ListAdmin(ctx context.Context, q, status string, limit, offset int) ([]*domain.Venue, error)
	CountAdmin(ctx context.Context, q, status string) (int, error)
}

var venueStatuses = map[string]bool{
	"active": true, "under_review": true, "disabled": true, "archived": true,
}

var organizerStatuses = map[string]bool{
	"active": true, "suspended": true, "archived": true,
}

var applicationStatuses = map[string]bool{
	"pending": true, "approved": true, "rejected": true,
	"needs_more_information": true, "withdrawn": true,
}

var eventStatuses = map[string]bool{
	"draft": true, "published": true, "cancelled": true, "completed": true, "archived": true,
}

var moderationStatuses = map[string]bool{
	"clean": true, "reported": true, "under_review": true, "blocked": true, "restored": true,
}

type AdminService struct {
	reports     reportAdminStore
	events      eventStore
	apps        applicationAdminStore
	adminEvents adminEventsStore
	orgs        adminOrganizerStore
	users       adminUsersStore
	venues      adminVenueStore
	media       mediaCheckStore
}

func NewAdminService(reports reportAdminStore, events eventStore, apps applicationAdminStore, adminEvents adminEventsStore, orgs adminOrganizerStore, users adminUsersStore, venues adminVenueStore) *AdminService {
	return &AdminService{reports: reports, events: events, apps: apps, adminEvents: adminEvents, orgs: orgs, users: users, venues: venues}
}

func (s *AdminService) SetMediaStore(media mediaCheckStore) {
	s.media = media
}

func (s *AdminService) ListReports(ctx context.Context, page, limit int) (PageResult[*domain.Report], error) {
	offset := (page - 1) * limit
	items, err := s.reports.ListAll(ctx, limit, offset)
	if err != nil {
		return PageResult[*domain.Report]{}, err
	}
	total, err := s.reports.CountAll(ctx)
	if err != nil {
		return PageResult[*domain.Report]{}, err
	}
	return PageResult[*domain.Report]{Items: items, Total: total}, nil
}

func (s *AdminService) ListApplications(ctx context.Context, page, limit int, status string) (PageResult[*domain.OrganizerApplication], error) {
	if status != "" && !applicationStatuses[status] {
		return PageResult[*domain.OrganizerApplication]{},
			shared.NewAppError("validation_error", "status must be one of pending, approved, rejected, needs_more_information, withdrawn.", http.StatusUnprocessableEntity)
	}
	offset := (page - 1) * limit
	items, err := s.apps.ListApplications(ctx, status, limit, offset)
	if err != nil {
		return PageResult[*domain.OrganizerApplication]{}, err
	}
	total, err := s.apps.CountApplications(ctx, status)
	if err != nil {
		return PageResult[*domain.OrganizerApplication]{}, err
	}
	return PageResult[*domain.OrganizerApplication]{Items: items, Total: total}, nil
}

func adminEventFilterError(value string) error {
	return shared.NewAppError("validation_error", value+" is not a valid filter value.", http.StatusUnprocessableEntity)
}

func (s *AdminService) ListEvents(ctx context.Context, page, limit int, status, moderation string) (PageResult[*domain.Event], error) {
	if status != "" && !eventStatuses[status] {
		return PageResult[*domain.Event]{}, adminEventFilterError("status")
	}
	if moderation != "" && !moderationStatuses[moderation] {
		return PageResult[*domain.Event]{}, adminEventFilterError("moderation_status")
	}
	offset := (page - 1) * limit
	items, err := s.adminEvents.AdminList(ctx, status, moderation, limit, offset)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	total, err := s.adminEvents.AdminCount(ctx, status, moderation)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	return PageResult[*domain.Event]{Items: items, Total: total}, nil
}

func (s *AdminService) ResolveReport(ctx context.Context, id, resolution string) (*domain.Report, error) {
	if len(resolution) > 2000 {
		return nil, shared.NewAppError("validation_error", "resolution must be at most 2000 characters.", http.StatusUnprocessableEntity)
	}
	report, err := s.reports.Resolve(ctx, id, resolution)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("report_not_found", "No such open report.", http.StatusNotFound)
	}
	return report, err
}

func (s *AdminService) BlockEvent(ctx context.Context, eventID string) (*domain.Event, error) {
	if err := s.events.SetModeration(ctx, eventID, "blocked"); err != nil {
		return nil, err
	}
	return s.events.GetByID(ctx, eventID)
}

func (s *AdminService) RestoreEvent(ctx context.Context, eventID string) (*domain.Event, error) {
	if err := s.events.SetModeration(ctx, eventID, "clean"); err != nil {
		return nil, err
	}
	return s.events.GetByID(ctx, eventID)
}

func (s *AdminService) ListOrganizers(ctx context.Context, page, limit int, q, status string) (PageResult[*domain.Organizer], error) {
	if status != "" && !organizerStatuses[status] {
		return PageResult[*domain.Organizer]{}, adminEventFilterError("status")
	}
	q = strings.TrimSpace(q)
	if len(q) > 100 {
		return PageResult[*domain.Organizer]{},
			shared.NewAppError("validation_error", "search must be at most 100 characters.", http.StatusUnprocessableEntity)
	}
	offset := (page - 1) * limit
	items, err := s.orgs.ListAdmin(ctx, q, status, limit, offset)
	if err != nil {
		return PageResult[*domain.Organizer]{}, err
	}
	total, err := s.orgs.CountAdmin(ctx, q, status)
	if err != nil {
		return PageResult[*domain.Organizer]{}, err
	}
	return PageResult[*domain.Organizer]{Items: items, Total: total}, nil
}

func (s *AdminService) GetOrganizer(ctx context.Context, id string) (*domain.Organizer, error) {
	org, err := s.orgs.GetOrganizerByID(ctx, id)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("organizer_not_found", "No such organizer.", http.StatusNotFound)
	}
	return org, err
}

func (s *AdminService) OrganizerEventCount(ctx context.Context, organizerID string) (int, error) {
	return s.adminEvents.CountByOrganizer(ctx, organizerID)
}

func (s *AdminService) RecentOrganizerEvents(ctx context.Context, organizerID string, limit int) ([]*domain.Event, error) {
	return s.adminEvents.AdminListByOrganizer(ctx, organizerID, limit, 0)
}

func (s *AdminService) CreateOrganizer(ctx context.Context, adminUserID string, o *domain.Organizer) (*domain.Organizer, error) {
	v := validator.New()
	v.Required(o.Name, "name")
	v.MinChars(o.Name, 3, "name")
	v.MaxChars(o.Name, 80, "name")
	v.MaxChars(o.Bio, 2000, "bio")
	v.MaxChars(o.Slug, 60, "slug")
	if o.Status != "" && !organizerStatuses[o.Status] {
		v.AddError("status", "must be one of active, suspended, archived")
	}
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}

	o.Name = strings.TrimSpace(o.Name)
	slug := slugify(firstNonEmpty(o.Slug, o.Name))
	if slug == "" {
		return nil, shared.NewAppError("validation_error", "A valid slug could not be derived from the name.", http.StatusUnprocessableEntity)
	}
	o.Slug = slug

	if taken, err := s.orgs.GetOrganizerBySlug(ctx, o.Slug); err == nil && taken != nil {
		return nil, shared.NewAppError("slug_taken", "That slug is already in use.", http.StatusConflict)
	} else if err != nil && err != shared.ErrNotFound {
		return nil, err
	}

	if o.OwnerUserID != nil {
		if _, err := s.users.GetByID(ctx, *o.OwnerUserID); err != nil {
			if err == shared.ErrNotFound {
				return nil, shared.NewAppError("user_not_found", "The linked owner user does not exist.", http.StatusUnprocessableEntity)
			}
			return nil, err
		}
		if owned, err := s.orgs.GetOrganizerByOwner(ctx, *o.OwnerUserID); err == nil && owned != nil {
			return nil, shared.NewAppError("organizer_exists", "That user already owns an organizer profile.", http.StatusConflict)
		} else if err != nil && err != shared.ErrNotFound {
			return nil, err
		}
	}

	if o.LogoMediaID != nil {
		if err := s.requireAdminMedia(ctx, adminUserID, *o.LogoMediaID, domain.MediaKindOrganizerLogo); err != nil {
			return nil, err
		}
	}

	if o.Status == "" {
		o.Status = "active"
	}
	return s.orgs.CreateOrganizer(ctx, o)
}

func (s *AdminService) requireAdminMedia(ctx context.Context, adminUserID, mediaID, kind string) error {
	if s.media == nil {
		return shared.NewAppError("media_unavailable", "Media attachments are not available.", http.StatusUnprocessableEntity)
	}
	asset, err := s.media.GetAssetByID(ctx, mediaID)
	if err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("invalid_media", "The referenced media asset does not exist.", http.StatusUnprocessableEntity)
		}
		return err
	}
	if asset.UploaderID != adminUserID {
		return shared.NewAppError("invalid_media", "The media asset was not uploaded by the acting admin.", http.StatusUnprocessableEntity)
	}
	if asset.Kind != kind {
		return shared.NewAppError("invalid_media", "The media asset is not the right kind for this field.", http.StatusUnprocessableEntity)
	}
	if asset.Status != domain.MediaStatusReady {
		return shared.NewAppError("invalid_media", "The media asset has not finished processing.", http.StatusUnprocessableEntity)
	}
	return nil
}

var userRoles = map[string]bool{
	"user": true, "admin": true,
}

var userStatuses = map[string]bool{
	"active": true, "suspended": true, "deactivated": true,
}

func (s *AdminService) ListUsers(ctx context.Context, page, limit int, q, role, status string) (PageResult[*domain.User], error) {
	if role != "" && !userRoles[role] {
		return PageResult[*domain.User]{}, adminEventFilterError("role")
	}
	if status != "" && !userStatuses[status] {
		return PageResult[*domain.User]{}, adminEventFilterError("status")
	}
	q = strings.TrimSpace(q)
	if len(q) > 100 {
		return PageResult[*domain.User]{},
			shared.NewAppError("validation_error", "search must be at most 100 characters.", http.StatusUnprocessableEntity)
	}
	offset := (page - 1) * limit
	items, err := s.users.ListAdmin(ctx, q, role, status, limit, offset)
	if err != nil {
		return PageResult[*domain.User]{}, err
	}
	total, err := s.users.CountAdmin(ctx, q, role, status)
	if err != nil {
		return PageResult[*domain.User]{}, err
	}
	return PageResult[*domain.User]{Items: items, Total: total}, nil
}

func (s *AdminService) UpdateUser(ctx context.Context, actingAdminID, targetID string, username, bio, photoURL, role, status *string) (*domain.User, error) {
	v := validator.New()
	if username != nil {
		v.Required(*username, "username")
		v.MinChars(*username, 3, "username")
		v.MaxChars(*username, 32, "username")
	}
	if bio != nil {
		v.MaxChars(*bio, 500, "bio")
	}
	if photoURL != nil {
		v.MaxChars(*photoURL, 1000, "photo_url")
	}
	if role != nil && !userRoles[*role] {
		v.AddError("role", "must be one of user, admin")
	}
	if status != nil && !userStatuses[*status] {
		v.AddError("status", "must be one of active, suspended, deactivated")
	}
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}

	if username == nil && bio == nil && photoURL == nil && role == nil && status == nil {
		return nil, shared.NewAppError("validation_error", "nothing to update", http.StatusUnprocessableEntity)
	}

	if _, err := s.users.GetByID(ctx, targetID); err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("user_not_found", "No such user.", http.StatusNotFound)
		}
		return nil, err
	}

	if (role != nil || status != nil) && actingAdminID == targetID {
		return nil, shared.NewAppError("self_modification", "An admin cannot change their own role or status.", http.StatusUnprocessableEntity)
	}

	user, err := s.users.UpdateAdmin(ctx, targetID, username, bio, photoURL, role, status)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// SuspendUser deactivates a user's account (blocks further logins via the
// existing auth status check). Idempotent on an already-suspended account.
func (s *AdminService) SuspendUser(ctx context.Context, actingAdminID, targetID string) (*domain.User, error) {
	if actingAdminID == targetID {
		return nil, shared.NewAppError("self_modification", "An admin cannot suspend their own account.", http.StatusUnprocessableEntity)
	}
	return s.setUserStatus(ctx, targetID, "suspended")
}

func (s *AdminService) RestoreUser(ctx context.Context, targetID string) (*domain.User, error) {
	return s.setUserStatus(ctx, targetID, "active")
}

func (s *AdminService) setUserStatus(ctx context.Context, targetID, status string) (*domain.User, error) {
	user, err := s.users.SetStatus(ctx, targetID, status)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("user_not_found", "No such user.", http.StatusNotFound)
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AdminService) ListVenues(ctx context.Context, page, limit int, q, status string) (PageResult[*domain.Venue], error) {
	if status != "" && !venueStatuses[status] {
		return PageResult[*domain.Venue]{},
			shared.NewAppError("validation_error", "status must be one of active, under_review, disabled, archived", http.StatusUnprocessableEntity)
	}
	q = strings.TrimSpace(q)
	if len(q) > 100 {
		return PageResult[*domain.Venue]{},
			shared.NewAppError("validation_error", "search must be at most 100 characters.", http.StatusUnprocessableEntity)
	}
	offset := (page - 1) * limit
	items, err := s.venues.ListAdmin(ctx, q, status, limit, offset)
	if err != nil {
		return PageResult[*domain.Venue]{}, err
	}
	total, err := s.venues.CountAdmin(ctx, q, status)
	if err != nil {
		return PageResult[*domain.Venue]{}, err
	}
	return PageResult[*domain.Venue]{Items: items, Total: total}, nil
}

func (s *AdminService) CreateVenue(ctx context.Context, organizerID string, v *domain.Venue) (*domain.Venue, error) {
	if _, err := s.orgs.GetOrganizerByID(ctx, organizerID); err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("organizer_not_found", "No such organizer.", http.StatusUnprocessableEntity)
		}
		return nil, err
	}

	val := validator.New()
	val.Required(v.Name, "name")
	val.MaxChars(v.Name, 200, "name")
	val.Check(v.Latitude >= -90 && v.Latitude <= 90, "latitude", "must be between -90 and 90")
	val.Check(v.Longitude >= -180 && v.Longitude <= 180, "longitude", "must be between -180 and 180")
	val.MaxChars(v.Address, 500, "address")
	val.MaxChars(v.PlaceID, 200, "place_id")
	val.MaxChars(v.City, 100, "city")
	val.MaxChars(v.CountryCode, 100, "country_code")
	if !val.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(val), http.StatusUnprocessableEntity)
	}

	v.OrganizerID = &organizerID
	return s.venues.Create(ctx, v)
}

type AdminEventDetail struct {
	Event     *domain.Event
	Organizer *domain.Organizer
	Venue     *domain.Venue
}

type AdminEventCreateInput struct {
	OrganizerID  *string
	NewOrganizer *domain.Organizer
	VenueID      *string
	NewVenue     *domain.Venue
	Event        *domain.Event
	Publish      bool
}

type AdminOrganizerAssignInput struct {
	OrganizerID  *string
	NewOrganizer *domain.Organizer
}

func (s *AdminService) GetEventDetail(ctx context.Context, eventID string) (*AdminEventDetail, error) {
	event, err := s.events.GetByID(ctx, eventID)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("event_not_found", "No such event.", http.StatusNotFound)
	}
	if err != nil {
		return nil, err
	}
	detail := &AdminEventDetail{Event: event}
	if org, err := s.orgs.GetOrganizerByID(ctx, event.OrganizerID); err == nil {
		detail.Organizer = org
	} else if err != shared.ErrNotFound {
		return nil, err
	}
	if event.VenueID != nil {
		if venue, err := s.venues.GetByID(ctx, *event.VenueID); err == nil {
			detail.Venue = venue
		} else if err != shared.ErrNotFound {
			return nil, err
		}
	}
	return detail, nil
}

func (s *AdminService) CreateEvent(ctx context.Context, adminUserID string, in *AdminEventCreateInput) (*domain.Event, error) {
	org, err := s.resolveOrganizer(ctx, adminUserID, &AdminOrganizerAssignInput{
		OrganizerID:  in.OrganizerID,
		NewOrganizer: in.NewOrganizer,
	})
	if err != nil {
		return nil, err
	}
	if err := validateEvent(in.Event); err != nil {
		return nil, err
	}
	if in.Event.PosterMediaID != nil {
		if err := s.requireAdminMedia(ctx, adminUserID, *in.Event.PosterMediaID, domain.MediaKindEventPoster); err != nil {
			return nil, err
		}
	}
	if in.Event.TeaserMediaID != nil {
		if err := s.requireAdminMedia(ctx, adminUserID, *in.Event.TeaserMediaID, domain.MediaKindEventTeaser); err != nil {
			return nil, err
		}
	}

	in.Event.OrganizerID = org.ID
	switch {
	case in.VenueID != nil:
		if err := s.requireVenueExists(ctx, *in.VenueID); err != nil {
			return nil, err
		}
		in.Event.VenueID = in.VenueID
	case in.NewVenue != nil:
		v := in.NewVenue
		v.OrganizerID = &org.ID
		if err := s.validateVenue(v); err != nil {
			return nil, err
		}
		created, err := s.venues.Create(ctx, v)
		if err != nil {
			return nil, err
		}
		in.Event.VenueID = &created.ID
	default:
		if in.Publish {
			return nil, shared.NewAppError("venue_required", "A venue is required before publishing.", http.StatusUnprocessableEntity)
		}
	}

	in.Event.Status = "draft"
	created, err := s.events.Create(ctx, in.Event)
	if err != nil {
		return nil, err
	}
	if in.Publish {
		if err := s.events.UpdateStatus(ctx, created.ID, "published"); err != nil {
			return nil, err
		}
		created.Status = "published"
	}
	return created, nil
}

func (s *AdminService) validateVenue(v *domain.Venue) error {
	val := validator.New()
	val.Required(v.Name, "name")
	val.MaxChars(v.Name, 200, "name")
	val.Check(v.Latitude >= -90 && v.Latitude <= 90, "latitude", "must be between -90 and 90")
	val.Check(v.Longitude >= -180 && v.Longitude <= 180, "longitude", "must be between -180 and 180")
	val.MaxChars(v.Address, 500, "address")
	val.MaxChars(v.PlaceID, 200, "place_id")
	val.MaxChars(v.City, 100, "city")
	val.MaxChars(v.CountryCode, 100, "country_code")
	if !val.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(val), http.StatusUnprocessableEntity)
	}
	return nil
}

func (s *AdminService) ReassignOrganizer(ctx context.Context, adminUserID, eventID string, in *AdminOrganizerAssignInput) (*domain.Event, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("event_not_found", "No such event.", http.StatusNotFound)
		}
		return nil, err
	}
	org, err := s.resolveOrganizer(ctx, adminUserID, in)
	if err != nil {
		return nil, err
	}
	updated, err := s.adminEvents.UpdateOrganizer(ctx, eventID, org.ID)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("event_not_found", "No such event.", http.StatusNotFound)
	}
	return updated, err
}

func (s *AdminService) resolveOrganizer(ctx context.Context, adminUserID string, in *AdminOrganizerAssignInput) (*domain.Organizer, error) {
	if in.OrganizerID != nil {
		org, err := s.orgs.GetOrganizerByID(ctx, *in.OrganizerID)
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("organizer_not_found", "No such organizer.", http.StatusUnprocessableEntity)
		}
		return org, err
	}
	if in.NewOrganizer != nil {
		return s.CreateOrganizer(ctx, adminUserID, in.NewOrganizer)
	}
	return nil, shared.NewAppError("organizer_required", "An organizer id or inline organizer is required.", http.StatusUnprocessableEntity)
}

func (s *AdminService) UpdateEventAdmin(ctx context.Context, eventID string, e *domain.Event) (*domain.Event, error) {
	existing, err := s.events.GetByID(ctx, eventID)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("event_not_found", "No such event.", http.StatusNotFound)
	}
	if err != nil {
		return nil, err
	}
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	if e.VenueID != nil {
		if err := s.requireVenueExists(ctx, *e.VenueID); err != nil {
			return nil, err
		}
	}
	if e.PosterMediaID != nil {
		if err := s.requireMediaAsset(ctx, *e.PosterMediaID, domain.MediaKindEventPoster); err != nil {
			return nil, err
		}
	}
	if e.TeaserMediaID != nil {
		if err := s.requireMediaAsset(ctx, *e.TeaserMediaID, domain.MediaKindEventTeaser); err != nil {
			return nil, err
		}
	}
	e.ID = existing.ID
	return s.events.Update(ctx, e)
}

func (s *AdminService) PublishEventAdmin(ctx context.Context, eventID string) (*domain.Event, error) {
	existing, err := s.eventForAdmin(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if existing.VenueID == nil {
		return nil, shared.NewAppError("venue_required", "A venue is required before publishing.", http.StatusUnprocessableEntity)
	}
	if existing.Status != "draft" && existing.Status != "cancelled" {
		return nil, shared.NewAppError("event_not_draft", "Only draft or cancelled events can be published.", http.StatusConflict)
	}
	if err := s.events.UpdateStatus(ctx, eventID, "published"); err != nil {
		return nil, err
	}
	existing.Status = "published"
	return existing, nil
}

func (s *AdminService) CancelEventAdmin(ctx context.Context, eventID string) (*domain.Event, error) {
	existing, err := s.eventForAdmin(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if existing.Status != "draft" && existing.Status != "published" {
		return nil, shared.NewAppError("event_not_cancellable", "Only draft or published events can be cancelled.", http.StatusConflict)
	}
	if err := s.events.UpdateStatus(ctx, eventID, "cancelled"); err != nil {
		return nil, err
	}
	existing.Status = "cancelled"
	return existing, nil
}

func (s *AdminService) eventForAdmin(ctx context.Context, eventID string) (*domain.Event, error) {
	existing, err := s.events.GetByID(ctx, eventID)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("event_not_found", "No such event.", http.StatusNotFound)
	}
	return existing, err
}

func (s *AdminService) requireVenueExists(ctx context.Context, venueID string) error {
	if _, err := s.venues.GetByID(ctx, venueID); err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("venue_not_found", "No such venue.", http.StatusUnprocessableEntity)
		}
		return err
	}
	return nil
}

func (s *AdminService) requireMediaAsset(ctx context.Context, mediaID, kind string) error {
	if s.media == nil {
		return shared.NewAppError("media_unavailable", "Media attachments are not available.", http.StatusUnprocessableEntity)
	}
	asset, err := s.media.GetAssetByID(ctx, mediaID)
	if err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("invalid_media", "The referenced media asset does not exist.", http.StatusUnprocessableEntity)
		}
		return err
	}
	if asset.Kind != kind {
		return shared.NewAppError("invalid_media", "The media asset is not the right kind for this field.", http.StatusUnprocessableEntity)
	}
	if asset.Status != domain.MediaStatusReady {
		return shared.NewAppError("invalid_media", "The media asset has not finished processing.", http.StatusUnprocessableEntity)
	}
	return nil
}
