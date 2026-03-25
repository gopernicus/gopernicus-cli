// This file is created once by gopernicus and will NOT be overwritten.
// Add custom store methods here. Store is defined in generated.go.

package serviceaccountspgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/core/repositories/auth/serviceaccounts"
)

// Create inserts a new ServiceAccount, automatically creating the required principal first.
// Uses a transaction to ensure the principal and service account are created atomically.
func (s *Store) Create(ctx context.Context, input serviceaccounts.CreateServiceAccount) (serviceaccounts.ServiceAccount, error) {
	now := time.Now().UTC()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return serviceaccounts.ServiceAccount{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	principalQuery := `INSERT INTO public.principals (principal_id, principal_type, created_at)
		VALUES (@principal_id, @principal_type, @created_at)
		ON CONFLICT (principal_id) DO NOTHING`

	principalArgs := pgx.NamedArgs{
		"principal_id":   input.ServiceAccountID,
		"principal_type": "service_account",
		"created_at":     now,
	}

	_, err = tx.Exec(ctx, principalQuery, principalArgs)
	if err != nil {
		return serviceaccounts.ServiceAccount{}, s.mapError(err)
	}

	query := `INSERT INTO public.service_accounts (service_account_id, name, description, creator_principal_id, record_state, created_at, updated_at)
		VALUES (@service_account_id, @name, @description, @creator_principal_id, @record_state, @_created_at, @_updated_at)
		RETURNING service_account_id, name, description, creator_principal_id, record_state, created_at, updated_at`

	args := pgx.NamedArgs{
		"service_account_id":   input.ServiceAccountID,
		"name":                 input.Name,
		"description":          input.Description,
		"creator_principal_id": input.CreatorPrincipalID,
		"record_state":         input.RecordState,
		"_created_at":          now,
		"_updated_at":          now,
	}

	rows, err := tx.Query(ctx, query, args)
	if err != nil {
		return serviceaccounts.ServiceAccount{}, s.mapError(err)
	}
	defer rows.Close()

	record, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[serviceaccounts.ServiceAccount])
	if err != nil {
		return serviceaccounts.ServiceAccount{}, s.mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return serviceaccounts.ServiceAccount{}, fmt.Errorf("commit transaction: %w", err)
	}

	return record, nil
}
