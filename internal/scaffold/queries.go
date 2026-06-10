package scaffold

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// Queries generates a default queries.sql with CRUD operations.
//
// Data annotations only — no protocol annotations (@http:json, @authenticated,
// @authorize, @auth.create). Protocol config lives in bridge.yml.
//
// Generated operations (when applicable):
//   List, Get, Create, Update, SoftDelete, Archive, Restore, Delete
func Queries(
	table *schema.TableInfo,
	tableName, entitySingular string,
	anc Ancestry,
) string {
	pkColumn := ""
	if table.PrimaryKey != nil {
		pkColumn = table.PrimaryKey.Column
	}

	// Tenant scoping: always add tenant_id to WHERE clauses for list/get.
	tenantCol := ""
	if anc.Tenant != nil {
		tenantCol = anc.Tenant.Column
	}

	// Build WHERE scope parts for all ancestry params.
	// These are appended to every query to enforce tenant + parent isolation.
	var scopeParts []string
	if tenantCol != "" {
		scopeParts = append(scopeParts, fmt.Sprintf("%s = @%s", tenantCol, tenantCol))
	}
	parentCol := ""
	if anc.Parent != nil {
		parentCol = anc.Parent.Column
		scopeParts = append(scopeParts, fmt.Sprintf("%s = @%s", parentCol, parentCol))
	}

	// For list WHERE: "tenant_id = @tenant_id AND parent_question_id = @parent_question_id AND "
	scopeWhere := ""
	if len(scopeParts) > 0 {
		scopeWhere = strings.Join(scopeParts, " AND ") + " AND "
	}

	// For single-record WHERE: " AND tenant_id = @tenant_id AND parent_question_id = @parent_question_id"
	scopeAndClause := ""
	if len(scopeParts) > 0 {
		scopeAndClause = " AND " + strings.Join(scopeParts, " AND ")
	}

	isTenantScoped := tenantCol != ""

	hasSlug := detectSlug(table)

	hasSoftDelete := false
	var searchCols []string
	var filterExclusions []string
	var orderExclusions []string
	var createExclusions []string
	var updateExclusions []string

	for _, col := range table.Columns {
		if col.Name == "record_state" {
			hasSoftDelete = true
		}

		if !col.IsPrimaryKey && !col.IsEnum && !col.IsForeignKey && isStringType(col) {
			if col.Name != "record_state" && !isHashOrSecret(col.Name) {
				searchCols = append(searchCols, col.Name)
			}
		}

		if col.Name == "created_at" || col.Name == "updated_at" {
			createExclusions = append(createExclusions, "-"+col.Name)
		} else if col.IsAutoIncrement {
			createExclusions = append(createExclusions, "-"+col.Name)
		}

		if col.IsPrimaryKey {
			updateExclusions = append(updateExclusions, "-"+col.Name)
		} else if col.Name == "created_at" {
			updateExclusions = append(updateExclusions, "-created_at")
		} else if col.Name == "record_state" {
			updateExclusions = append(updateExclusions, "-record_state")
		} else if isTenantScoped && col.Name == tenantCol {
			updateExclusions = append(updateExclusions, "-"+tenantCol)
		} else if anc.Parent != nil && col.Name == anc.Parent.Column {
			updateExclusions = append(updateExclusions, "-"+anc.Parent.Column)
		}
	}

	filterSpec := buildSpec("*", filterExclusions)
	orderSpec := buildSpec("*", orderExclusions)

	var b strings.Builder

	// ─── List ────────────────────────────────────────────────────────────
	b.WriteString("-- @func: List\n")
	fmt.Fprintf(&b, "-- @filter:conditions %s\n", filterSpec)
	if len(searchCols) > 0 {
		fmt.Fprintf(&b, "-- @search: ilike(%s)\n", strings.Join(searchCols, ", "))
	}
	fmt.Fprintf(&b, "-- @order: %s\n", orderSpec)
	b.WriteString("-- @max: 100\n")
	fmt.Fprintf(&b, "SELECT *\nFROM %s\n", tableName)
	if len(searchCols) > 0 {
		fmt.Fprintf(&b, "WHERE %s$conditions AND $search\n", scopeWhere)
	} else {
		fmt.Fprintf(&b, "WHERE %s$conditions\n", scopeWhere)
	}
	b.WriteString("ORDER BY $order\n")
	b.WriteString("LIMIT $limit\n")
	b.WriteString(";\n\n")

	// ─── Get ─────────────────────────────────────────────────────────────
	if pkColumn != "" {
		b.WriteString("-- @func: Get\n")
		fmt.Fprintf(&b, "SELECT *\nFROM %s\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\n;\n\n")
	}

	// ─── GetBySlug / GetIDBySlug ─────────────────────────────────────────
	if hasSlug && pkColumn != "" {
		softDeleteFilter := "\nAND record_state = 'active'"
		if !hasSoftDelete {
			softDeleteFilter = ""
		}
		b.WriteString("-- @func: GetBySlug\n")
		fmt.Fprintf(&b, "SELECT *\nFROM %s\nWHERE slug = @slug%s%s\n;\n\n", tableName, scopeAndClause, softDeleteFilter)

		b.WriteString("-- @func: GetIDBySlug\n")
		fmt.Fprintf(&b, "-- @returns: %s\n", pkColumn)
		fmt.Fprintf(&b, "SELECT %s\nFROM %s\nWHERE slug = @slug%s%s\n;\n\n", pkColumn, tableName, scopeAndClause, softDeleteFilter)
	}

	// ─── Create ──────────────────────────────────────────────────────────
	createSpec := buildSpec("*", createExclusions)
	b.WriteString("-- @func: Create\n")
	fmt.Fprintf(&b, "-- @fields: %s\n", createSpec)
	fmt.Fprintf(&b, "INSERT INTO %s\n($fields)\nVALUES ($values)\nRETURNING *;\n\n", tableName)

	// ─── Update ──────────────────────────────────────────────────────────
	if pkColumn != "" {
		updateSpec := buildSpec("*", updateExclusions)
		b.WriteString("-- @func: Update\n")
		fmt.Fprintf(&b, "-- @fields: %s\n", updateSpec)
		fmt.Fprintf(&b, "UPDATE %s\nSET $fields\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\nRETURNING *;\n\n")
	}

	// ─── SoftDelete / Archive / Restore ─────────────────────────────────
	if pkColumn != "" && hasSoftDelete {
		pkAndScope := fmt.Sprintf("%s = @%s%s", pkColumn, pkColumn, scopeAndClause)

		b.WriteString("-- @func: SoftDelete\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'deleted'\nWHERE %s\n;\n\n", tableName, pkAndScope)

		b.WriteString("-- @func: Archive\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'archived'\nWHERE %s\n;\n\n", tableName, pkAndScope)

		b.WriteString("-- @func: Restore\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'active'\nWHERE %s\n;\n\n", tableName, pkAndScope)
	}

	// ─── Delete ──────────────────────────────────────────────────────────
	if pkColumn != "" {
		b.WriteString("-- @func: Delete\n")
		fmt.Fprintf(&b, "DELETE FROM %s\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\n;\n\n")
	}

	return b.String()
}

// detectSlug returns true if the table has a globally unique slug column —
// i.e. a slug column with a single-column unique constraint. Composite unique
// slugs (e.g. UNIQUE(tenant_id, slug)) are not detected; those require a
// custom GetBySlug query written by the developer.
func detectSlug(table *schema.TableInfo) bool {
	hasSlugCol := false
	for _, col := range table.Columns {
		if col.Name == "slug" && (col.GoType == "string" || col.GoType == "*string") {
			hasSlugCol = true
			if col.IsUnique {
				return true // unique flag set directly on the column
			}
			break
		}
	}
	if !hasSlugCol {
		return false
	}
	// Also check indexes: a unique index where slug is the only column.
	// Skip partial indexes — those encode business rules we can't generalize.
	for _, idx := range table.Indexes {
		if !idx.Unique || idx.Predicate != "" {
			continue
		}
		if len(idx.Columns) == 1 && idx.Columns[0] == "slug" {
			return true
		}
	}
	return false
}

// buildSpec builds a column spec string.
// If base is "*" and there are exclusions, returns "*,-col1,-col2".
// If base is "" and exclusions is empty, returns just "*".
func buildSpec(base string, exclusions []string) string {
	if len(exclusions) == 0 {
		return base
	}
	return base + "," + strings.Join(exclusions, ",")
}

// isStringType returns true if the column is a string/text type (for search candidates).
func isStringType(col schema.ColumnInfo) bool {
	goType := strings.TrimPrefix(col.GoType, "*")
	return goType == "string"
}

// isHashOrSecret returns true if the column name suggests it holds a hash or secret.
func isHashOrSecret(name string) bool {
	return strings.Contains(name, "hash") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "token") ||
		strings.Contains(name, "password") ||
		strings.Contains(name, "key_prefix")
}
