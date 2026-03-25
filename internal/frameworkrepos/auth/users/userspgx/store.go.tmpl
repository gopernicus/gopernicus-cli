// This file is created once by gopernicus and will NOT be overwritten.
// Add custom store methods here. Store is defined in generated.go.

package userspgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
)

// Create inserts a new User, automatically creating the required principal first.
// Uses a transaction to ensure the principal and user are created atomically.
func (s *Store) Create(ctx context.Context, input users.CreateUser) (users.User, error) {
	now := time.Now().UTC()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return users.User{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert principal first (ON CONFLICT DO NOTHING handles edge cases)
	principalQuery := `INSERT INTO public.principals (principal_id, principal_type, created_at)
		VALUES (@principal_id, @principal_type, @created_at)
		ON CONFLICT (principal_id) DO NOTHING`

	principalArgs := pgx.NamedArgs{
		"principal_id":   input.UserID,
		"principal_type": "user",
		"created_at":     now,
	}

	_, err = tx.Exec(ctx, principalQuery, principalArgs)
	if err != nil {
		return users.User{}, s.mapError(err)
	}

	// Insert user
	query := `INSERT INTO public.users (user_id, email, display_name, email_verified, last_login_at, record_state, created_at, updated_at)
		VALUES (@user_id, @email, @display_name, @email_verified, @last_login_at, @record_state, @_created_at, @_updated_at)
		RETURNING user_id, email, display_name, email_verified, last_login_at, record_state, created_at, updated_at`

	args := pgx.NamedArgs{
		"user_id":        input.UserID,
		"email":          input.Email,
		"display_name":   input.DisplayName,
		"email_verified": input.EmailVerified,
		"last_login_at":  input.LastLoginAt,
		"record_state":   input.RecordState,
		"_created_at":    now,
		"_updated_at":    now,
	}

	rows, err := tx.Query(ctx, query, args)
	if err != nil {
		return users.User{}, s.mapError(err)
	}
	defer rows.Close()

	record, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[users.User])
	if err != nil {
		return users.User{}, s.mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return users.User{}, fmt.Errorf("commit transaction: %w", err)
	}

	return record, nil
}
