// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package users

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// CreateUser is the input for creating a user.
// Defined manually because user creation requires a transactional principal insert.
type CreateUser struct {
	UserID        string     `json:"user_id,omitempty"`
	Email         string     `json:"email,omitempty"`
	DisplayName   *string    `json:"display_name,omitempty"`
	EmailVerified bool       `json:"email_verified,omitempty"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	RecordState   string     `json:"record_state,omitempty"`
}

// UserCreatedEvent is emitted when a user is created.
type UserCreatedEvent struct {
	events.BaseEvent
	User User `json:"user"`
}

// =============================================================================
// Storer
// =============================================================================

// Storer defines the user data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// Custom: transactional create (principal + user)
	Create(ctx context.Context, input CreateUser) (User, error)

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]User, error)
	Get(ctx context.Context, userID string) (User, error)
	Update(ctx context.Context, userID string, input UpdateUser) (User, error)
	SoftDelete(ctx context.Context, userID string) error
	Archive(ctx context.Context, userID string) error
	Restore(ctx context.Context, userID string) error
	Delete(ctx context.Context, userID string) error
	GetByEmail(ctx context.Context, email string) (User, error)
	SetEmailVerified(ctx context.Context, updatedAt time.Time, userID string) error
	SetLastLogin(ctx context.Context, lastLoginAt time.Time, updatedAt time.Time, userID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for Users.
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

// NewRepository creates a new User repository.
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
// Custom method because user creation requires a transactional principal insert.
func (r *Repository) Create(ctx context.Context, input CreateUser) (User, error) {
	if input.UserID == "" {
		id, err := r.generateID()
		if err != nil {
			return User{}, fmt.Errorf("generate id: %w", err)
		}
		input.UserID = id
	}
	if input.RecordState == "" {
		input.RecordState = "active"
	}

	user, err := r.store.Create(ctx, input)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}

	if r.bus != nil {
		r.bus.Emit(ctx, UserCreatedEvent{
			BaseEvent: events.NewBaseEvent("user.created"),
			User:      user,
		})
	}

	return user, nil
}

// Ensure imports are used.
var _ context.Context
var _ fop.Order
