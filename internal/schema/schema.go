// Package schema defines the types for workshop/public.json.
// Produced by 'gopernicus db reflect', consumed by 'gopernicus generate'.
package schema

import "time"

// ReflectedSchema is the root of workshop/public.json.
type ReflectedSchema struct {
	Version     string                   `json:"version"`
	Source      string                   `json:"source"`       // e.g. "postgres"
	Database    string                   `json:"database"`     // database name
	SchemaName  string                   `json:"schema_name"`  // e.g. "public"
	ReflectedAt time.Time                `json:"reflected_at"`
	Tables      map[string]*TableInfo    `json:"tables"`
	EnumTypes   map[string]*EnumTypeInfo `json:"enum_types,omitempty"`
}

// EnumTypeInfo represents a PostgreSQL ENUM type definition.
type EnumTypeInfo struct {
	Name   string   `json:"name"`
	Schema string   `json:"schema"`
	Values []string `json:"values"`
}

// TableInfo represents a single table's metadata.
type TableInfo struct {
	TableName   string           `json:"table_name"`
	Schema      string           `json:"schema"`
	PrimaryKey  *PrimaryKeyInfo  `json:"primary_key"`
	Columns     []ColumnInfo     `json:"columns"`
	ForeignKeys []ForeignKeyInfo `json:"foreign_keys"`
	Indexes     []IndexInfo      `json:"indexes"`
	Constraints []ConstraintInfo `json:"constraints"`
	Comment     string           `json:"comment,omitempty"`
}

// ColumnInfo represents a single column's metadata.
type ColumnInfo struct {
	Name           string `json:"name"`
	DBType         string `json:"db_type"`           // e.g. "uuid", "varchar(255)"
	GoType         string `json:"go_type"`           // e.g. "string", "*time.Time"
	GoImport       string `json:"go_import"`         // import path if needed, e.g. "time"
	IsNullable     bool   `json:"is_nullable"`
	IsPrimaryKey   bool   `json:"is_primary_key"`
	IsForeignKey   bool   `json:"is_foreign_key"`
	IsUnique       bool   `json:"is_unique,omitempty"`
	DefaultValue   string `json:"default_value,omitempty"`
	HasDefault     bool   `json:"has_default"`
	MaxLength      int    `json:"max_length,omitempty"`
	Precision      int    `json:"precision,omitempty"`
	Scale          int    `json:"scale,omitempty"`
	ValidationTags string `json:"validation_tags,omitempty"`
	Comment        string `json:"comment,omitempty"`

	// ENUM support
	IsEnum     bool     `json:"is_enum,omitempty"`
	EnumType   string   `json:"enum_type,omitempty"`
	EnumValues []string `json:"enum_values,omitempty"`

	// Auto-increment support (SERIAL, BIGSERIAL, IDENTITY)
	IsAutoIncrement   bool   `json:"is_auto_increment,omitempty"`
	AutoIncrementType string `json:"auto_increment_type,omitempty"` // "SERIAL", "BIGSERIAL", "IDENTITY"
}

// PrimaryKeyInfo represents primary key metadata.
type PrimaryKeyInfo struct {
	Column      string   `json:"column"`                  // first column (backwards compat)
	Columns     []string `json:"columns,omitempty"`       // all columns (composite PKs)
	DBType      string   `json:"db_type"`
	GoType      string   `json:"go_type"`
	HasDefault  bool     `json:"has_default"`
	DefaultExpr string   `json:"default_expr,omitempty"`
}

// ForeignKeyInfo represents a foreign key relationship.
type ForeignKeyInfo struct {
	ConstraintName string   `json:"constraint_name"`
	Columns        []string `json:"columns"`
	RefTable       string   `json:"ref_table"`
	RefSchema      string   `json:"ref_schema"`
	RefColumns     []string `json:"ref_columns"`
	OnDelete       string   `json:"on_delete"` // CASCADE, SET_NULL, RESTRICT, NO_ACTION
	OnUpdate       string   `json:"on_update"`

	// Backwards compat: first column (single-column FKs).
	ColumnName string `json:"column_name"`
	RefColumn  string `json:"ref_column"`
}

// IndexInfo represents an index.
type IndexInfo struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	Unique     bool     `json:"unique"`
	Method     string   `json:"method"`               // btree, hash, gin, gist, etc.
	Predicate  string   `json:"predicate,omitempty"`   // WHERE clause for partial indexes
	Definition string   `json:"definition,omitempty"`  // full CREATE INDEX statement (for expression indexes)
}

// ConstraintInfo represents a table constraint (CHECK, UNIQUE, EXCLUDE).
type ConstraintInfo struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`            // CHECK, UNIQUE, EXCLUDE
	Definition string   `json:"definition"`      // the constraint expression
	Columns    []string `json:"columns,omitempty"`
}
