package scaffold

import (
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// ParentInfo holds a detected parent FK for an entity.
type ParentInfo struct {
	Column   string // FK column name, e.g. "parent_question_id"
	RefTable string // referenced table, e.g. "questions"
	RelName  string // singularized ref table, e.g. "question"
}

// Ancestry holds the detected tenant and parent relationships for an entity.
type Ancestry struct {
	Tenant *ParentInfo // tenant FK (tenant_id → tenants), nil if not tenant-scoped
	Parent *ParentInfo // direct parent FK (parent_ prefix), nil if none
}

// DetectAncestry finds tenant and parent relationships for a table.
//
// Returns both independently:
//   - Tenant: tenant_id FK referencing the tenants table (isolation boundary)
//   - Parent: FK column prefixed with parent_ (direct parent for create/list scoping)
//
// An entity can have both (e.g. takes has tenant_id + parent_question_id),
// just tenant (e.g. questions has tenant_id only), just parent (e.g. api_keys
// has parent_service_account_id), or neither.
func DetectAncestry(table *schema.TableInfo) Ancestry {
	fkByColumn := make(map[string]schema.ForeignKeyInfo)
	for _, fk := range table.ForeignKeys {
		col := fk.ColumnName
		if len(fk.Columns) > 0 {
			col = fk.Columns[0]
		}
		fkByColumn[col] = fk
	}

	var a Ancestry

	// Tenant scoping: tenant_id → tenants.
	if fk, ok := fkByColumn["tenant_id"]; ok && fk.RefTable == "tenants" {
		a.Tenant = &ParentInfo{
			Column:   "tenant_id",
			RefTable: "tenants",
			RelName:  "tenant",
		}
	}

	// Direct parent: FK column with parent_ prefix (excluding tenant).
	for col, fk := range fkByColumn {
		if !strings.HasPrefix(col, "parent_") {
			continue
		}
		if fk.RefTable == "tenants" {
			// parent_tenant_id → tenants is a tenant, not a generic parent.
			if a.Tenant == nil {
				a.Tenant = &ParentInfo{
					Column:   col,
					RefTable: "tenants",
					RelName:  "tenant",
				}
			}
			continue
		}
		a.Parent = &ParentInfo{
			Column:   col,
			RefTable: fk.RefTable,
			RelName:  generators.Singularize(fk.RefTable),
		}
	}

	return a
}
