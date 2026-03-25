// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package sessions

import (
	"context"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the session data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, parentUserID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]Session, error)
	Get(ctx context.Context, sessionID string, parentUserID string) (Session, error)
	Create(ctx context.Context, input CreateSession) (Session, error)
	Update(ctx context.Context, sessionID string, parentUserID string, input UpdateSession) (Session, error)
	Delete(ctx context.Context, sessionID string, parentUserID string) error
	GetByTokenHash(ctx context.Context, sessionTokenHash string) (Session, error)
	GetByRefreshHash(ctx context.Context, refreshTokenHash string) (Session, error)
	GetByPreviousRefreshHash(ctx context.Context, previousRefreshTokenHash string) (Session, error)
	DeleteAllForUser(ctx context.Context, parentUserID string) error
	DeleteAllForUserExcept(ctx context.Context, parentUserID string, sessionID string) error
	UpdateByID(ctx context.Context, sessionID string, input UpdateSession) (Session, error)
	DeleteByID(ctx context.Context, sessionID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for Sessions.
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

// NewRepository creates a new Session repository.
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
