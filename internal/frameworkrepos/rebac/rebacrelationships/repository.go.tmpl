// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.

package rebacrelationships

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the rebac_relationship data access contract.
type Storer interface {
	// Authorization check queries (hand-written — recursive CTEs, scalar returns).
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RebacRelationship, error)
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)

	// Authorization batch operations (hand-written — UNNEST pattern).
	BulkCreate(ctx context.Context, inputs []CreateRebacRelationship) ([]RebacRelationship, error)

	// LookupResources queries (hand-written — recursive CTEs, []string returns).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error)
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error)

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]RebacRelationship, error)
	Get(ctx context.Context, relationshipID string) (RebacRelationship, error)
	Create(ctx context.Context, input CreateRebacRelationship) (RebacRelationship, error)
	Update(ctx context.Context, relationshipID string, input UpdateRebacRelationship) (RebacRelationship, error)
	Delete(ctx context.Context, relationshipID string) error
	ListBySubject(ctx context.Context, filter FilterListBySubject, subjectType string, subjectID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]RebacRelationship, error)
	ListByResource(ctx context.Context, filter FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]RebacRelationship, error)
	DeleteAllForResource(ctx context.Context, resourceType string, resourceID string) error
	DeleteByTuple(ctx context.Context, resourceType string, resourceID string, relation string, subjectType string, subjectID string) error
	DeleteByResourceAndSubject(ctx context.Context, resourceType string, resourceID string, subjectType string, subjectID string) error
	DeleteBySubject(ctx context.Context, subjectType string, subjectID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for RebacRelationships.
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

// NewRepository creates a new RebacRelationship repository.
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

// =============================================================================
// Custom Delegation (hand-written store methods → Repository API)
// =============================================================================

func (r *Repository) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	result, err := r.store.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID)
	if err != nil {
		return false, fmt.Errorf("check relation with group expansion: %w", err)
	}
	return result, nil
}

func (r *Repository) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	result, err := r.store.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
	if err != nil {
		return false, fmt.Errorf("check relation exists: %w", err)
	}
	return result, nil
}

func (r *Repository) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	result, err := r.store.CheckBatchDirect(ctx, resourceType, resourceIDs, relation, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("check batch direct: %w", err)
	}
	return result, nil
}

func (r *Repository) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RebacRelationship, error) {
	result, err := r.store.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, fmt.Errorf("get relation targets: %w", err)
	}
	return result, nil
}

func (r *Repository) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	result, err := r.store.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
	if err != nil {
		return 0, fmt.Errorf("count by resource and relation: %w", err)
	}
	return result, nil
}

func (r *Repository) BulkCreate(ctx context.Context, inputs []CreateRebacRelationship) ([]RebacRelationship, error) {
	result, err := r.store.BulkCreate(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("bulk create: %w", err)
	}
	return result, nil
}

func (r *Repository) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	result, err := r.store.LookupResourceIDs(ctx, resourceType, relations, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("lookup resource ids: %w", err)
	}
	return result, nil
}

func (r *Repository) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	result, err := r.store.LookupResourceIDsByRelationTarget(ctx, resourceType, relation, targetType, targetIDs)
	if err != nil {
		return nil, fmt.Errorf("lookup resource ids by relation target: %w", err)
	}
	return result, nil
}

// Ensure imports are used.
var _ context.Context
var _ fop.Order
