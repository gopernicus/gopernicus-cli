// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package invitations

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

// Storer defines the invitation data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]Invitation, error)
	Get(ctx context.Context, invitationID string) (Invitation, error)
	Create(ctx context.Context, input CreateInvitation) (Invitation, error)
	Update(ctx context.Context, invitationID string, input UpdateInvitation) (Invitation, error)
	SoftDelete(ctx context.Context, invitationID string) error
	Archive(ctx context.Context, invitationID string) error
	Restore(ctx context.Context, invitationID string) error
	Delete(ctx context.Context, invitationID string) error
	GetByToken(ctx context.Context, tokenHash string, now time.Time) (Invitation, error)
	ListByResource(ctx context.Context, filter FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]Invitation, error)
	ListBySubject(ctx context.Context, filter FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]Invitation, error)
	ListByIdentifier(ctx context.Context, filter FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]Invitation, error)
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for Invitations.
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

// NewRepository creates a new Invitation repository.
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
