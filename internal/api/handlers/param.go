package handlers

import (
	"net/http"
	"regexp"
)

// uuidPathRe matches the canonical 8-4-4-4-12 hex form emitted by PostgreSQL's
// gen_random_uuid() (any case). Any non-matching value is rejected before it
// reaches the database, where Postgres would otherwise raise a 22P02
// "invalid input syntax for type uuid" that surfaces to the client as a 500.
var uuidPathRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ParseUUIDParam validates a UUID path parameter (e.g. {id}) before it reaches
// the database. It returns the matched value and whether it is a well-formed
// UUID. Callers must write a 404 when ok is false; the check happens before
// any DB call so malformed ids can never produce a 22P02 500.
//
// The project models ids as plain strings (no uuid dependency); the strict
// regex intentionally admits only the canonical form the server itself emits.
func ParseUUIDParam(r *http.Request, name string) (string, bool) {
	v := r.PathValue(name)
	return v, uuidPathRe.MatchString(v)
}
