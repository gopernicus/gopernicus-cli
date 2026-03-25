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

func TestFuncExists(t *testing.T) {
	src := `package example

func NewRepository(store Storer) *Repository {
	return &Repository{store: store}
}

func helperFunc() {}

func (r *Repository) Create() {}
`
	path := writeTestFile(t, src)

	tests := []struct {
		funcName string
		want     bool
	}{
		{"NewRepository", true},
		{"helperFunc", true},
		{"Create", false},       // method, not function
		{"DoesNotExist", false},
	}

	for _, tt := range tests {
		got, err := FuncExists(path, tt.funcName)
		if err != nil {
			t.Errorf("FuncExists(%q): %v", tt.funcName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FuncExists(%q) = %v, want %v", tt.funcName, got, tt.want)
		}
	}
}

func TestFuncExists_FileNotFound(t *testing.T) {
	got, err := FuncExists("/nonexistent/path.go", "Func")
	if err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
	if got {
		t.Error("expected false for missing file")
	}
}

func TestTypeExists(t *testing.T) {
	src := `package example

type Repository struct {
	store Storer
}

type Storer interface {
	Get(ctx context.Context, id string) (Entity, error)
}

type Option func(*Repository)
`
	path := writeTestFile(t, src)

	tests := []struct {
		typeName string
		want     bool
	}{
		{"Repository", true},
		{"Storer", true},
		{"Option", true},
		{"DoesNotExist", false},
	}

	for _, tt := range tests {
		got, err := TypeExists(path, tt.typeName)
		if err != nil {
			t.Errorf("TypeExists(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("TypeExists(%q) = %v, want %v", tt.typeName, got, tt.want)
		}
	}
}

func TestTypeExists_FileNotFound(t *testing.T) {
	got, err := TypeExists("/nonexistent/path.go", "Type")
	if err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
	if got {
		t.Error("expected false for missing file")
	}
}
