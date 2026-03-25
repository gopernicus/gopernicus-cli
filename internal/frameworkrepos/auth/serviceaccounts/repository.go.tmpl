// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.

package serviceaccounts

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// CreateServiceAccount is the input for creating a service account.
// Defined manually because creation requires a transactional principal insert.
type CreateServiceAccount struct {
	ServiceAccountID   string  `json:"service_account_id,omitempty"`
	Name               string  `json:"name,omitempty"`
	Description        *string `json:"description,omitempty"`
	CreatorPrincipalID string  `json:"creator_principal_id,omitempty"`
	RecordState        string  `json:"record_state,omitempty"`
}

// ServiceAccountCreatedEvent is emitted when a service account is created.
type ServiceAccountCreatedEvent struct {
	events.BaseEvent
	ServiceAccount ServiceAccount `json:"service_account"`
}

// =============================================================================
// Storer
// =============================================================================

// Storer defines the service account data access contract.
type Storer interface {
	// Custom: transactional create (principal + service account)
	Create(ctx context.Context, input CreateServiceAccount) (ServiceAccount, error)

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]ServiceAccount, error)
	Get(ctx context.Context, serviceAccountID string) (ServiceAccount, error)
	Update(ctx context.Context, serviceAccountID string, input UpdateServiceAccount) (ServiceAccount, error)
	SoftDelete(ctx context.Context, serviceAccountID string) error
	Archive(ctx context.Context, serviceAccountID string) error
	Restore(ctx context.Context, serviceAccountID string) error
	Delete(ctx context.Context, serviceAccountID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for ServiceAccounts.
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

// NewRepository creates a new ServiceAccount repository.
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

// Create generates an ID, sets defaults, and delegates to the store.
// Custom method because service account creation requires a transactional principal insert.
func (r *Repository) Create(ctx context.Context, input CreateServiceAccount) (ServiceAccount, error) {
	if input.ServiceAccountID == "" {
		id, err := r.generateID()
		if err != nil {
			return ServiceAccount{}, fmt.Errorf("generate id: %w", err)
		}
		input.ServiceAccountID = id
	}
	if input.RecordState == "" {
		input.RecordState = "active"
	}

	sa, err := r.store.Create(ctx, input)
	if err != nil {
		return ServiceAccount{}, fmt.Errorf("create service account: %w", err)
	}

	if r.bus != nil {
		r.bus.Emit(ctx, ServiceAccountCreatedEvent{
			BaseEvent:      events.NewBaseEvent("service_account.created"),
			ServiceAccount: sa,
		})
	}

	return sa, nil
}

// Ensure imports are used.
var _ context.Context
var _ fop.Order
