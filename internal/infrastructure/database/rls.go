package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetRLSContext sets transaction-local app context so RLS policies can
// evaluate current_app_user_id()/current_app_role(). It must be called at the
// start of a transaction; the settings are discarded on commit/rollback so
// pooled connections never leak authorization context.
func SetRLSContext(ctx context.Context, pool *pgxpool.Pool, userID, role string) error {
	_, err := pool.Exec(ctx, `
		SELECT set_config('app.user_id', $1, true),
		       set_config('app.role', $2, true)`,
		userID, role)
	if err != nil {
		return fmt.Errorf("set RLS context: %w", err)
	}
	return nil
}

// SetRLSContextTx applies the app context on an existing transaction.
func SetRLSContextTx(ctx context.Context, q Querier, userID, role string) error {
	_, err := q.Exec(ctx, `
		SELECT set_config('app.user_id', $1, true),
		       set_config('app.role', $2, true)`,
		userID, role)
	if err != nil {
		return fmt.Errorf("set RLS context on tx: %w", err)
	}
	return nil
}
