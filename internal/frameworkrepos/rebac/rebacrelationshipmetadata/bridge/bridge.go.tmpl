// Package rebacrelationshipmetadatabridge provides HTTP bridge for RebacRelationshipMetadata operations.
// This file is created once and can be customized.
// Add custom options, fields, or middleware helpers here.

package rebacrelationshipmetadatabridge

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/bridge/protocol/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/core/repositories/rebac/rebacrelationshipmetadata"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// Bridge provides HTTP handlers for RebacRelationshipMetadata operations.
type Bridge struct {
	rebacRelationshipMetadataRepository *rebacrelationshipmetadata.Repository
	log                                 *slog.Logger
	rateLimiter                         *ratelimiter.RateLimiter
	authenticator                       *authentication.Authenticator
	authorizer                          *authorization.Authorizer
	jsonErrors                          httpmid.ErrorRenderer
	htmlErrors                          httpmid.ErrorRenderer
}

// BridgeOption configures optional Bridge dependencies.
type BridgeOption func(*Bridge)

// WithJSONErrorRenderer overrides the default JSON error renderer.
func WithJSONErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
	return func(b *Bridge) { b.jsonErrors = r }
}

// WithHTMLErrorRenderer sets the HTML error renderer for server-rendered routes.
func WithHTMLErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
	return func(b *Bridge) { b.htmlErrors = r }
}

// NewBridge creates a new Bridge with the given dependencies.
func NewBridge(
	log *slog.Logger,
	rebacRelationshipMetadataRepository *rebacrelationshipmetadata.Repository,
	rateLimiter *ratelimiter.RateLimiter,
	authenticator *authentication.Authenticator,
	authorizer *authorization.Authorizer,
	opts ...BridgeOption,
) *Bridge {
	b := &Bridge{
		rebacRelationshipMetadataRepository: rebacRelationshipMetadataRepository,
		log:                                 log,
		rateLimiter:                         rateLimiter,
		authenticator:                       authenticator,
		authorizer:                          authorizer,
		jsonErrors:                          httpmid.JSONErrors{},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}
