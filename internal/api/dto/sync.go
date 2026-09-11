package dto

// SyncChange is one delta entry. Op is "upsert" (payload is the row DTO) or
// "delete" (payload null; the client drops its local copy of id).
type SyncChange struct {
	Domain  string `json:"domain"`
	Op      string `json:"op"`
	ID      string `json:"id"`
	Payload any    `json:"payload"`
}

// SyncData is the GET /api/v1/sync payload. next_cursor is always present and
// inclusive: the next poll with cursor=next_cursor re-includes the boundary
// rows. has_more is emitted (per domain) only while pages remain; when absent
// every requested domain is exhausted.
type SyncData struct {
	Changes    []SyncChange    `json:"changes"`
	NextCursor string          `json:"next_cursor"`
	HasMore    map[string]bool `json:"has_more,omitempty"`
}
