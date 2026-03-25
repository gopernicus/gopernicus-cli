// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package apikeys

import (
	"context"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the api_key data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, parentServiceAccountID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]APIKey, error)
	Get(ctx context.Context, apiKeyID string, parentServiceAccountID string) (APIKey, error)
	Create(ctx context.Context, input CreateAPIKey) (APIKey, error)
	Update(ctx context.Context, apiKeyID string, parentServiceAccountID string, input UpdateAPIKey) (APIKey, error)
	SoftDelete(ctx context.Context, apiKeyID string, parentServiceAccountID string) error
	Archive(ctx context.Context, apiKeyID string, parentServiceAccountID string) error
	Restore(ctx context.Context, apiKeyID string, parentServiceAccountID string) error
	Delete(ctx context.Context, apiKeyID string, parentServiceAccountID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for APIKeys.
type Repository struct {
	store      Storer
	generateID func() (string, error)
	bus        events.Bus
}

// Option configures a Repository.
type Option func(*Repository)

// WithGenerateID overrides the default ID generator (cryptids.GenerateID).
func WithGenerateID(fn func() (string, error)) Option {
	return func(r *Repository) { r.generateID = fn }
}

// WithEventBus configures the event bus for emitting domain events.
func WithEventBus(bus events.Bus) Option {
	return func(r *Repository) { r.bus = bus }
}

// NewRepository creates a new APIKey repository.
func NewRepository(store Storer, opts ...Option) *Repository {
	r := &Repository{
		store:      store,
		generateID: cryptids.GenerateID,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Ensure imports are used.
var _ context.Context
var _ fop.Order
