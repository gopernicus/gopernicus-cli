package generators

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// Deterministically-invalid samples per validator. The validation helpers
// are validate-if-set (empty passes everything except Required), so each
// negative case mutates exactly one field on a known-valid baseline.
const (
	badEnumValue  = "zzz-not-an-allowed-value"
	badEmailValue = "not-an-email"
	badURLValue   = "not-a-url"
	badSlugValue  = "Not A Slug!"
	badUUIDValue  = "not-a-uuid"
)

// TestFieldAssign is one field assignment in the valid-request literal.
type TestFieldAssign struct {
	GoName    string
	ValueExpr string
}

// ValidationTestCase is one negative table entry: assigning ValueExpr to
// GoName on a valid request must make Validate() return an error.
type ValidationTestCase struct {
	Name      string
	GoName    string
	ValueExpr string
}

// BridgeValidationTestData renders generated_validation_test.go.
type BridgeValidationTestData struct {
	BridgePackage string
	EntityName    string

	HasCreate          bool
	CreateValidAssigns []TestFieldAssign
	CreateCases        []ValidationTestCase

	HasUpdate   bool
	UpdateCases []ValidationTestCase

	NeedsStrings bool
	NeedsTestPtr bool
}

// GenerateBridgeValidationTests writes pure-Go unit tests for the generated
// request-model Validate methods. Regenerated on every run; removed when the
// entity no longer has create/update request models.
func GenerateBridgeValidationTests(data BridgeTemplateData, bridgeDir string, opts Options) error {
	path := filepath.Join(bridgeDir, "generated_validation_test.go")

	testData := buildBridgeValidationTestData(data)
	if !testData.HasCreate && !testData.HasUpdate {
		if fileExists(path) && !opts.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale generated_validation_test.go: %w", err)
			}
		}
		return nil
	}

	tmpl, err := template.New("bridge_validation_test").Parse(bridgeValidationTestTemplate)
	if err != nil {
		return fmt.Errorf("parse validation test template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, testData); err != nil {
		return fmt.Errorf("render validation tests: %w", err)
	}
	if err := renderGoFile("validation tests", buf.Bytes(), path, opts); err != nil {
		return err
	}
	fmt.Printf("      write %s\n", path)
	return nil
}

func buildBridgeValidationTestData(data BridgeTemplateData) BridgeValidationTestData {
	out := BridgeValidationTestData{
		BridgePackage: data.BridgePackage,
		EntityName:    data.EntityName,
	}

	if len(data.CreateQueries) > 0 {
		out.HasCreate = true
		for _, f := range data.CreateQueries[0].Fields {
			// The primary key is server-generated — never pin it in the valid
			// baseline, or repeated creates (e.g. the pagination e2e) collide.
			if f.DBName == data.PKColumn {
				continue
			}
			if assign, ok := validCreateAssign(f); ok {
				out.CreateValidAssigns = append(out.CreateValidAssigns, assign)
			}
			out.CreateCases = append(out.CreateCases, createValidationCases(f)...)
		}
	}

	if len(data.UpdateQueries) > 0 {
		out.HasUpdate = true
		for _, f := range data.UpdateQueries[0].Fields {
			out.UpdateCases = append(out.UpdateCases, updateValidationCases(f)...)
		}
	}

	for _, c := range out.CreateCases {
		if containsRepeat(c.ValueExpr) {
			out.NeedsStrings = true
		}
		if containsTestPtr(c.ValueExpr) {
			out.NeedsTestPtr = true
		}
	}
	for _, c := range out.UpdateCases {
		if containsRepeat(c.ValueExpr) {
			out.NeedsStrings = true
		}
	}
	if len(out.UpdateCases) > 0 {
		out.NeedsTestPtr = true
	}

	return out
}

// validCreateAssign returns the valid-baseline assignment for a constrained
// non-pointer string field. Unconstrained and pointer fields stay zero —
// every validator except Required passes on empty/nil.
func validCreateAssign(f BridgeField) (TestFieldAssign, bool) {
	if !f.IsString || f.IsPointer {
		return TestFieldAssign{}, false
	}
	constrained := f.IsRequired || f.IsEnum || f.IsEmail || f.IsURL || f.IsSlug || f.IsUUID || f.MaxLength > 0
	if !constrained {
		return TestFieldAssign{}, false
	}
	return TestFieldAssign{GoName: f.GoName, ValueExpr: validStringFor(f)}, true
}

// validStringFor picks a value satisfying every validator the bridge emits
// for the field (precedence mirrors specificity).
func validStringFor(f BridgeField) string {
	switch {
	case f.IsEnum && len(f.EnumValues) > 0:
		return strconv.Quote(f.EnumValues[0])
	case f.IsEmail:
		return `"user@example.com"`
	case f.IsURL:
		return `"https://example.com"`
	case f.IsSlug:
		return `"valid-slug"`
	case f.IsUUID:
		return `"123e4567-e89b-12d3-a456-426614174000"`
	case f.MaxLength == 1:
		return `"a"`
	default:
		return `"ok"`
	}
}

func createValidationCases(f BridgeField) []ValidationTestCase {
	if !f.IsString {
		return nil
	}
	wrap := func(expr string) string {
		if f.IsPointer {
			return "testPtr(" + expr + ")"
		}
		return expr
	}

	var cases []ValidationTestCase
	if f.IsRequired && !f.IsPointer {
		cases = append(cases, ValidationTestCase{
			Name:      "missing required " + f.DBName,
			GoName:    f.GoName,
			ValueExpr: `""`,
		})
	}
	cases = append(cases, badValueCases(f, wrap, "")...)
	return cases
}

func updateValidationCases(f BridgeField) []ValidationTestCase {
	// Update request fields are always pointers; the Ptr validators skip
	// nil, so only set-but-invalid values are testable. Required is not
	// enforced on update.
	if !f.IsString {
		return nil
	}
	wrap := func(expr string) string { return "testPtr(" + expr + ")" }
	return badValueCases(f, wrap, "update ")
}

func badValueCases(f BridgeField, wrap func(string) string, prefix string) []ValidationTestCase {
	var cases []ValidationTestCase
	add := func(name, expr string) {
		cases = append(cases, ValidationTestCase{
			Name:      prefix + name,
			GoName:    f.GoName,
			ValueExpr: wrap(expr),
		})
	}

	if f.IsEnum && len(f.EnumValues) > 0 {
		add(f.DBName+" rejects value outside enum", strconv.Quote(badEnumValue))
	}
	if f.IsEmail {
		add(f.DBName+" rejects malformed email", strconv.Quote(badEmailValue))
	}
	if f.IsURL {
		add(f.DBName+" rejects malformed url", strconv.Quote(badURLValue))
	}
	if f.IsSlug {
		add(f.DBName+" rejects malformed slug", strconv.Quote(badSlugValue))
	}
	if f.IsUUID {
		add(f.DBName+" rejects malformed uuid", strconv.Quote(badUUIDValue))
	}
	if f.MaxLength > 0 {
		add(f.DBName+" rejects value over max length",
			fmt.Sprintf(`strings.Repeat("a", %d)`, f.MaxLength+1))
	}
	return cases
}

func containsRepeat(expr string) bool  { return strings.Contains(expr, "strings.Repeat(") }
func containsTestPtr(expr string) bool { return strings.Contains(expr, "testPtr(") }
