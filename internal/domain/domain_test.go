package domain

import "testing"

func TestRoleConstants(t *testing.T) {
	if string(RoleUser) != "user" {
		t.Fatalf("expected RoleUser to be 'user', got %q", RoleUser)
	}
	if string(RoleAdmin) != "admin" {
		t.Fatalf("expected RoleAdmin to be 'admin', got %q", RoleAdmin)
	}
	if RoleUser == RoleAdmin {
		t.Fatal("expected distinct user/admin roles")
	}
}

func TestUserZeroValueGuards(t *testing.T) {
	var u User
	if u.IsVerified {
		t.Fatal("zero-value user must not be verified")
	}
	if u.Role != "" {
		t.Fatalf("zero-value user must have empty role, got %q", u.Role)
	}
	if u.DeletedAt != nil {
		t.Fatal("zero-value user must not be soft-deleted")
	}
}

func TestUserFieldsRoundTrip(t *testing.T) {
	u := User{ID: "u1", Email: "e@x.co", Username: "ish", Role: RoleAdmin, IsVerified: true}
	if u.ID != "u1" || u.Email != "e@x.co" || u.Username != "ish" {
		t.Fatalf("unexpected user fields: %+v", u)
	}
	if u.Role != RoleAdmin || !u.IsVerified {
		t.Fatalf("unexpected role/verification: %+v", u)
	}
}

func TestEventOptionalPointersNilByDefault(t *testing.T) {
	var e Event
	if e.EndsAt != nil {
		t.Fatal("ends_at must be nil when unset")
	}
	if e.DeletedAt != nil {
		t.Fatal("deleted_at must be nil when unset")
	}
	if e.CategoryID != nil || e.VenueID != nil {
		t.Fatal("optional IDs must be nil when unset")
	}
	if e.MaxAttendees != nil {
		t.Fatal("max_attendees must be nil when unset")
	}
}

func TestSessionRevokedAtNilByDefault(t *testing.T) {
	var s Session
	if s.RevokedAt != nil {
		t.Fatal("revoked_at must be nil for an active session")
	}
}

func TestOrganizerOwnerIsOptional(t *testing.T) {
	var o Organizer
	if o.OwnerUserID != nil {
		t.Fatal("owner_user_id must be nil when unset (e.g., admin-created)")
	}
	if o.Status != "" {
		t.Fatalf("default status must be empty, got %q", o.Status)
	}
}
