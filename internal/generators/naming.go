package generators

import (
	"path/filepath"
	"strings"
)

// ToPascalCase converts a snake_case string to PascalCase.
func ToPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		switch strings.ToLower(part) {
		case "id":
			parts[i] = "ID"
		case "url":
			parts[i] = "URL"
		case "api":
			parts[i] = "API"
		default:
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

// ToCamelCase converts a snake_case string to camelCase.
func ToCamelCase(s string) string {
	pascal := ToPascalCase(s)
	if len(pascal) == 0 {
		return pascal
	}
	for i, r := range pascal {
		if i == 0 {
			continue
		}
		if r >= 'a' && r <= 'z' {
			if i == 1 {
				return strings.ToLower(pascal[:1]) + pascal[1:]
			}
			return strings.ToLower(pascal[:i-1]) + pascal[i-1:]
		}
	}
	return strings.ToLower(pascal)
}

// Singularize converts a plural word to singular form.
func Singularize(s string) string {
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "sses") || strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "zes") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return s[:len(s)-1]
	}
	return s
}

// Pluralize converts a singular word to plural form.
func Pluralize(s string) string {
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return s
	}
	if strings.HasSuffix(s, "y") && len(s) > 1 {
		prev := s[len(s)-2]
		if prev != 'a' && prev != 'e' && prev != 'i' && prev != 'o' && prev != 'u' {
			return s[:len(s)-1] + "ies"
		}
	}
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "sh") || strings.HasSuffix(s, "ch") ||
		strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") {
		return s + "es"
	}
	return s + "s"
}

// ToKebabCase converts a snake_case string to kebab-case.
func ToKebabCase(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}

// ToSpaced converts snake_case to lowercase with spaces.
func ToSpaced(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}

// PascalToSpaced converts PascalCase to "lower spaced" form.
// e.g. "GetUser" → "get user", "SoftDeleteUser" → "soft delete user".
func PascalToSpaced(s string) string {
	if s == "" {
		return s
	}
	var buf strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Don't split consecutive uppercase (e.g. "ID" stays together).
			nextIsLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
			prevIsLower := s[i-1] >= 'a' && s[i-1] <= 'z'
			if prevIsLower || nextIsLower {
				buf.WriteByte(' ')
			}
		}
		buf.WriteRune(r)
	}
	return strings.ToLower(buf.String())
}

// ToPackageName converts a table name to a valid Go package name.
func ToPackageName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// RepoPackage returns the Go package name for an entity's repository.
func RepoPackage(tableName string) string {
	return ToPackageName(tableName)
}

// RepoDir returns the directory path for an entity's repository.
func RepoDir(domainName, tableName, outputDir string) string {
	return filepath.Join(outputDir, "core", "repositories", domainName, ToPackageName(tableName))
}

// StorePackage returns the Go package name for a driver-specific store.
func StorePackage(tableName, driverSuffix string) string {
	return ToPackageName(tableName) + driverSuffix
}

// StoreDir returns the directory path for a driver-specific store.
func StoreDir(domainName, tableName, driverSuffix, outputDir string) string {
	pkg := ToPackageName(tableName)
	return filepath.Join(outputDir, "core", "repositories", domainName, pkg, pkg+driverSuffix)
}

// BridgePackage returns the Go package name for an entity's HTTP bridge.
func BridgePackage(tableName string) string {
	return ToPackageName(tableName) + "bridge"
}

// BridgeDir returns the directory path for an entity's HTTP bridge.
func BridgeDir(domainName, tableName, outputDir string) string {
	return filepath.Join(outputDir, "bridge", "repositories", BridgeCompositePackage(domainName), BridgePackage(tableName))
}

// FindPKParam returns the Go variable name of the param that matches the PK
// column, or empty string if not found. This matches by name (e.g. pkColumn
// "user_id" matches param "userID") rather than relying on position.
func FindPKParam(params []string, pkColumn string) string {
	pkCamel := ToCamelCase(pkColumn)
	for _, p := range params {
		if p == pkCamel {
			return p
		}
	}
	return ""
}
