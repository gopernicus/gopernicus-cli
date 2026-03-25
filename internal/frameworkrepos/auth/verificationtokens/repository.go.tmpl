// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package verificationtokens

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the verification_token data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]VerificationToken, error)
	Get(ctx context.Context, tokenID string) (VerificationToken, error)
	Create(ctx context.Context, input CreateVerificationToken) (VerificationToken, error)
	Update(ctx context.Context, tokenID string, input UpdateVerificationToken) (VerificationToken, error)
	Delete(ctx context.Context, tokenID string) error
	GetByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string, now time.Time) (VerificationToken, error)
	DeleteByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string) error
	DeleteByUserIDAndPurpose(ctx context.Context, userID string, purpose string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for VerificationTokens.
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

// NewRepository creates a new VerificationToken repository.
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
