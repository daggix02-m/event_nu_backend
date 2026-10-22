package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/repository"
)

// idempotencyTTL bounds how long a stored key is honored. It also acts as a
// safety valve: a key left 'in_progress' forever by a server failure expires
// and its row is reaped on the next Create, so the client can retry afresh.
const idempotencyTTL = 24 * time.Hour

// IdempotencyStore is the persistence surface the idempotency middleware needs.
// *repository.IdempotencyRepository satisfies it; tests substitute a fake.
type IdempotencyStore interface {
	Create(ctx context.Context, userID, key, requestHash, operation string, ttl time.Duration) error
	Get(ctx context.Context, userID, key string) (*repository.IdempotencyRecord, error)
	Complete(ctx context.Context, userID, key, status string, code int, body []byte) error
}

// Idempotency wraps a single state-changing write so a client-supplied
// Idempotency-Key makes the operation safe to retry (spec §22):
//   - no key            -> request passes through unchanged (opt-in);
//   - same key + sim req -> the stored status/body is replayed (no re-run);
//   - same key + diff req -> 409 idempotency_key_reused;
//   - racing same-key     -> 409 idempotency_in_progress (retry after settle);
//   - new key            -> runs the handler, then stores its response.
//
// It must run inside BeginRequestTx and after auth + SetRLS: the stored record
// shares the request transaction with the guarded write, so a failed (>=400)
// request rolls back the record too and the key is not recorded.
func Idempotency(keys IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			userID := UserID(r.Context())
			if userID == "" {
				writeServerError(w)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeBadRequest(w, "unable_to_read_body", "Could not read the request body.")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			operation := r.Method + " " + r.URL.Path
			hash := idempotencyHash(operation, body)

			if err := keys.Create(r.Context(), userID, key, hash, operation, idempotencyTTL); err != nil {
				if errors.Is(err, repository.ErrIdempotencyExists) {
					existing, gerr := keys.Get(r.Context(), userID, key)
					if gerr != nil {
						writeServerError(w)
						return
					}
					switch existing.Status {
					case repository.IdempotencyCompleted:
						if existing.RequestHash == hash {
							replay(w, existing)
						} else {
							writeConflict(w, "idempotency_key_reused",
								"idempotency key already used for a different request")
						}
					default:
						writeConflict(w, "idempotency_in_progress",
							"a request with this idempotency key is already in progress")
					}
					return
				}
				writeServerError(w)
				return
			}

			rec := &bufferingRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.status < http.StatusBadRequest {
				if cerr := keys.Complete(r.Context(), userID, key, repository.IdempotencyCompleted, rec.status, rec.body); cerr != nil {

					_ = cerr
				}
			}
		})
	}
}

// idempotencyHash is a deterministic sha256 of (method, path, raw body).
func idempotencyHash(operation string, body []byte) string {
	h := sha256.New()
	_, _ = io.WriteString(h, operation)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func replay(w http.ResponseWriter, rec *repository.IdempotencyRecord) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.ResponseCode)
	_, _ = w.Write(rec.ResponseBody)
}

type bufferingRecorder struct {
	http.ResponseWriter
	status      int
	WroteHeader bool
	body        []byte
}

func (b *bufferingRecorder) WriteHeader(status int) {
	if !b.WroteHeader {
		b.WroteHeader = true
		b.status = status
		b.ResponseWriter.WriteHeader(status)
	}
}

func (b *bufferingRecorder) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return b.ResponseWriter.Write(p)
}

func writeConflict(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}

func writeBadRequest(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
