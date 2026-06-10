package generators

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return path
}

func TestMethodExistsOnType(t *testing.T) {
	src := `package example

type Repository struct {
	store Storer
}

func (r *Repository) Create(ctx context.Context, input CreateUser) (User, error) {
	return User{}, nil
}

func (r *Repository) Get(ctx context.Context, userID string) (User, error) {
	return User{}, nil
}

func (r Repository) ValueReceiverMethod() string {
	return ""
}

func helperFunc() {}
`
	path := writeTestFile(t, src)

	tests := []struct {
		typeName   string
		methodName string
		want       bool
	}{
		{"Repository", "Create", true},
		{"Repository", "Get", true},
		{"Repository", "ValueReceiverMethod", true},
		{"Repository", "List", false},          // doesn't exist
		{"Repository", "helperFunc", false},     // not a method
		{"OtherType", "Create", false},          // wrong receiver type
	}

	for _, tt := range tests {
		got, err := MethodExistsOnType(path, tt.typeName, tt.methodName)
		if err != nil {
			t.Errorf("MethodExistsOnType(%q, %q): %v", tt.typeName, tt.methodName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MethodExistsOnType(%q, %q) = %v, want %v", tt.typeName, tt.methodName, got, tt.want)
		}
	}
}

func TestMethodExistsOnType_FileNotFound(t *testing.T) {
	got, err := MethodExistsOnType("/nonexistent/path.go", "Repository", "Create")
	if err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
	if got {
		t.Error("expected false for missing file")
	}
}
