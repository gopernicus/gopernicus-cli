package generators

import (
	"strings"
	"testing"
)

func TestReplaceMarkerBlockInSource(t *testing.T) {
	src := `type Storer interface {
	// Custom methods:
	GetByEmail(ctx context.Context, email string) (User, error)

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList) ([]User, error)
	Get(ctx context.Context, userID string) (User, error)
	// gopernicus:end
}
`
	newContent := "\tList(ctx context.Context, filter FilterList, orderBy fop.Order) ([]User, error)\n\tGet(ctx context.Context, userID string) (User, error)\n\tCreate(ctx context.Context, input CreateUser) (User, error)\n"

	result, err := replaceMarkerBlockInSource(src, newContent)
	if err != nil {
		t.Fatalf("replaceMarkerBlockInSource: %v", err)
	}

	// Custom method should be preserved.
	if !strings.Contains(result, "GetByEmail") {
		t.Error("custom method GetByEmail should be preserved")
	}

	// New content should be present.
	if !strings.Contains(result, "Create(ctx context.Context, input CreateUser)") {
		t.Error("new method Create should be present")
	}

	// Updated signature should be present.
	if !strings.Contains(result, "orderBy fop.Order") {
		t.Error("updated List signature should be present")
	}

	// Markers should still be present.
	if !strings.Contains(result, MarkerStart) {
		t.Error("start marker should be preserved")
	}
	if !strings.Contains(result, MarkerEnd) {
		t.Error("end marker should be preserved")
	}

	// Closing brace should still be there.
	if !strings.Contains(result, "\n}\n") {
		t.Error("interface closing brace should be preserved")
	}
}

func TestReplaceMarkerBlockInSource_NoStartMarker(t *testing.T) {
	src := `type Storer interface {
	Get(ctx context.Context, userID string) (User, error)
	// gopernicus:end
}
`
	_, err := replaceMarkerBlockInSource(src, "new content")
	if err == nil {
		t.Error("expected error for missing start marker")
	}
}

func TestReplaceMarkerBlockInSource_NoEndMarker(t *testing.T) {
	src := `type Storer interface {
	// gopernicus:start (DO NOT EDIT between markers)
	Get(ctx context.Context, userID string) (User, error)
}
`
	_, err := replaceMarkerBlockInSource(src, "new content")
	if err == nil {
		t.Error("expected error for missing end marker")
	}
}
