package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// Snapshot fields with no SQLite equivalent are set to their zero values:
//
//   - EnumTypes / ColumnInfo.IsEnum — SQLite has no enum types; none are emitted.
//   - TableInfo.Constraints — CHECK/UNIQUE table constraints are only visible
//     in the raw CREATE TABLE SQL (no PRAGMA exposes them); UNIQUE constraints
//     still surface through index_list (origin "u"), so uniqueness is captured.
//   - IndexInfo.Predicate — partial-index WHERE clauses are only present in
//     the CREATE INDEX SQL, which is preserved verbatim in IndexInfo.Definition.
//   - Column and table comments — SQLite has no comment metadata.
//   - ColumnInfo.Precision / Scale — declared types like NUMERIC(10,2) have no
//     enforced precision in SQLite; only MaxLength is parsed (for CHAR types).
//
// AutoIncrementType uses SQLite-specific values: "ROWID" for an INTEGER
// PRIMARY KEY rowid alias, "AUTOINCREMENT" when the keyword is present.

// reflectSchema reads the live schema via PRAGMA queries and returns a
// ReflectedSchema shaped identically to the Postgres reflector's output.
func reflectSchema(ctx context.Context, db *sql.DB, dbName, schemaName string) (*schema.ReflectedSchema, error) {
	result := &schema.ReflectedSchema{
		Version:     "1.0",
		Source:      "sqlite",
		Database:    dbName,
		SchemaName:  schemaName,
		ReflectedAt: time.Now(),
		Tables:      make(map[string]*schema.TableInfo),
		EnumTypes:   make(map[string]*schema.EnumTypeInfo),
	}

	tables, err := getTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("get tables: %w", err)
	}

	for _, t := range tables {
		table := &schema.TableInfo{
			TableName:   t.name,
			Schema:      schemaName,
			Columns:     []schema.ColumnInfo{},
			ForeignKeys: []schema.ForeignKeyInfo{},
			Indexes:     []schema.IndexInfo{},
			Constraints: []schema.ConstraintInfo{},
		}

		columns, err := getColumns(ctx, db, t.name)
		if err != nil {
			return nil, fmt.Errorf("get columns for %s: %w", t.name, err)
		}
		table.Columns = columns

		pk := buildPrimaryKey(columns, t.sql)
		if pk == nil {
			fmt.Printf("Warning: could not get primary key for %s.%s: no primary key found\n", schemaName, t.name)
		}
		table.PrimaryKey = pk

		fks, err := getForeignKeys(ctx, db, t.name, schemaName)
		if err != nil {
			return nil, fmt.Errorf("get foreign keys for %s: %w", t.name, err)
		}
		table.ForeignKeys = fks

		fkCols := make(map[string]bool)
		for _, fk := range fks {
			for _, c := range fk.Columns {
				fkCols[c] = true
			}
		}
		for i := range table.Columns {
			if fkCols[table.Columns[i].Name] {
				table.Columns[i].IsForeignKey = true
			}
		}

		indexes, err := getIndexes(ctx, db, t.name)
		if err != nil {
			return nil, fmt.Errorf("get indexes for %s: %w", t.name, err)
		}
		table.Indexes = indexes

		uniqueCols := make(map[string]bool)
		for _, idx := range indexes {
			if idx.Unique && len(idx.Columns) == 1 {
				uniqueCols[idx.Columns[0]] = true
			}
		}
		for i := range table.Columns {
			if uniqueCols[table.Columns[i].Name] {
				table.Columns[i].IsUnique = true
			}
		}

		result.Tables[t.name] = table
	}

	return result, nil
}

// ─── query helpers ────────────────────────────────────────────────────────────

// tableEntry is a user table from sqlite_master.
type tableEntry struct {
	name string
	sql  string // CREATE TABLE statement (used for AUTOINCREMENT detection)
}

func getTables(ctx context.Context, db *sql.DB) ([]tableEntry, error) {
	// sqlite_% tables (sqlite_sequence, sqlite_stat1, …) are internal.
	rows, err := db.QueryContext(ctx, `
		SELECT name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []tableEntry
	for rows.Next() {
		var t tableEntry
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func getColumns(ctx context.Context, db *sql.DB, tableName string) ([]schema.ColumnInfo, error) {
	// pragma_table_info is the table-valued form of PRAGMA table_info; unlike
	// the bare PRAGMA it accepts a bound parameter.
	rows, err := db.QueryContext(ctx, `
		SELECT name, type, "notnull", dflt_value, pk
		FROM pragma_table_info(?)
		ORDER BY cid
	`, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []schema.ColumnInfo
	for rows.Next() {
		var col schema.ColumnInfo
		var declType string
		var notNull, pkOrdinal int
		var defaultValue *string

		if err := rows.Scan(&col.Name, &declType, &notNull, &defaultValue, &pkOrdinal); err != nil {
			return nil, err
		}

		col.DBType = normalizeDeclaredType(declType)
		// pk is the column's 1-based ordinal within the primary key, 0 if not
		// part of it. PK columns are reported as not nullable.
		col.IsPrimaryKey = pkOrdinal > 0
		col.IsNullable = notNull == 0 && !col.IsPrimaryKey

		if defaultValue != nil {
			col.HasDefault = true
			col.DefaultValue = cleanDefault(*defaultValue)
		}

		if n := parseMaxLength(col.DBType); n > 0 {
			col.MaxLength = n
		}

		col.GoType, col.GoImport = mapDeclaredTypeToGo(col.DBType, col.Name, col.IsNullable)
		col.ValidationTags = deriveValidationTags(col)

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

// buildPrimaryKey assembles PrimaryKeyInfo from the pk ordinals already
// captured on the columns, and detects rowid-alias auto-increment from the
// CREATE TABLE SQL. Returns nil if the table has no primary key.
func buildPrimaryKey(columns []schema.ColumnInfo, createSQL string) *schema.PrimaryKeyInfo {
	var pkCols []string
	var first *schema.ColumnInfo
	for i := range columns {
		if columns[i].IsPrimaryKey {
			if first == nil {
				first = &columns[i]
			}
			pkCols = append(pkCols, columns[i].Name)
		}
	}
	if first == nil {
		return nil
	}

	// A single-column INTEGER PRIMARY KEY is an alias for rowid and is
	// auto-assigned on insert; the AUTOINCREMENT keyword additionally
	// prevents rowid reuse.
	if len(pkCols) == 1 && strings.EqualFold(first.DBType, "integer") {
		first.IsAutoIncrement = true
		if reAutoincrement.MatchString(createSQL) {
			first.AutoIncrementType = "AUTOINCREMENT"
		} else {
			first.AutoIncrementType = "ROWID"
		}
	}

	return &schema.PrimaryKeyInfo{
		Column:      pkCols[0],
		Columns:     pkCols,
		DBType:      first.DBType,
		GoType:      first.GoType,
		HasDefault:  first.HasDefault,
		DefaultExpr: first.DefaultValue,
	}
}

func getForeignKeys(ctx context.Context, db *sql.DB, tableName, schemaName string) ([]schema.ForeignKeyInfo, error) {
	// Rows sharing an id belong to one (possibly composite) FK; seq orders
	// the column pairs within it.
	rows, err := db.QueryContext(ctx, `
		SELECT id, "table", "from", "to", on_update, on_delete
		FROM pragma_foreign_key_list(?)
		ORDER BY id, seq
	`, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type fkRow struct {
		id                 int
		refTable, from     string
		to                 *string // NULL when the FK references the parent's implicit PK
		onUpdate, onDelete string
	}
	var fkRows []fkRow
	for rows.Next() {
		var r fkRow
		if err := rows.Scan(&r.id, &r.refTable, &r.from, &r.to, &r.onUpdate, &r.onDelete); err != nil {
			return nil, err
		}
		fkRows = append(fkRows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(fkRows) == 0 {
		return []schema.ForeignKeyInfo{}, nil
	}

	var fks []schema.ForeignKeyInfo
	for i := 0; i < len(fkRows); {
		j := i
		for j < len(fkRows) && fkRows[j].id == fkRows[i].id {
			j++
		}
		group := fkRows[i:j]
		i = j

		fk := schema.ForeignKeyInfo{
			RefTable:  group[0].refTable,
			RefSchema: schemaName,
			OnUpdate:  normalizeFKAction(group[0].onUpdate),
			OnDelete:  normalizeFKAction(group[0].onDelete),
		}
		for _, r := range group {
			fk.Columns = append(fk.Columns, r.from)
			if r.to != nil {
				fk.RefColumns = append(fk.RefColumns, *r.to)
			}
		}

		// "to" is NULL when the FK references the parent table's PK
		// implicitly (REFERENCES parent) — resolve it to the parent's PK.
		if len(fk.RefColumns) == 0 {
			refPK, err := getPrimaryKeyColumns(ctx, db, fk.RefTable)
			if err != nil {
				return nil, fmt.Errorf("resolve implicit FK target on %s: %w", fk.RefTable, err)
			}
			fk.RefColumns = refPK
		}

		// SQLite FKs are anonymous; synthesize a Postgres-style name so
		// downstream consumers see a stable identifier.
		fk.ConstraintName = tableName + "_" + strings.Join(fk.Columns, "_") + "_fkey"
		fk.ColumnName = fk.Columns[0]
		if len(fk.RefColumns) > 0 {
			fk.RefColumn = fk.RefColumns[0]
		}
		fks = append(fks, fk)
	}
	return fks, nil
}

func getPrimaryKeyColumns(ctx context.Context, db *sql.DB, tableName string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name
		FROM pragma_table_info(?)
		WHERE pk > 0
		ORDER BY pk
	`, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func getIndexes(ctx context.Context, db *sql.DB, tableName string) ([]schema.IndexInfo, error) {
	// origin: "c" = CREATE INDEX, "u" = UNIQUE constraint, "pk" = primary key.
	// PK indexes are excluded to match the Postgres reflector.
	rows, err := db.QueryContext(ctx, `
		SELECT name, "unique", origin
		FROM pragma_index_list(?)
		WHERE origin <> 'pk'
		ORDER BY name
	`, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type idxRow struct {
		name   string
		unique int
		origin string
	}
	var idxRows []idxRow
	for rows.Next() {
		var r idxRow
		if err := rows.Scan(&r.name, &r.unique, &r.origin); err != nil {
			return nil, err
		}
		idxRows = append(idxRows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(idxRows) == 0 {
		return []schema.IndexInfo{}, nil
	}

	var indexes []schema.IndexInfo
	for _, r := range idxRows {
		idx := schema.IndexInfo{
			Name:   r.name,
			Unique: r.unique == 1,
			// All SQLite indexes are b-trees.
			Method:  "btree",
			Columns: []string{},
		}

		colRows, err := db.QueryContext(ctx, `
			SELECT name
			FROM pragma_index_info(?)
			ORDER BY seqno
		`, r.name)
		if err != nil {
			return nil, fmt.Errorf("index info for %s: %w", r.name, err)
		}
		for colRows.Next() {
			var name *string // NULL for expression columns
			if err := colRows.Scan(&name); err != nil {
				colRows.Close()
				return nil, err
			}
			if name != nil {
				idx.Columns = append(idx.Columns, *name)
			} else {
				idx.Columns = append(idx.Columns, "<expression>")
			}
		}
		if err := colRows.Err(); err != nil {
			colRows.Close()
			return nil, err
		}
		colRows.Close()

		// Definition is the verbatim CREATE INDEX SQL; NULL for the
		// sqlite_autoindex_* indexes that back UNIQUE constraints.
		var defSQL *string
		err = db.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, r.name,
		).Scan(&defSQL)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("index sql for %s: %w", r.name, err)
		}
		if defSQL != nil {
			idx.Definition = *defSQL
		}

		indexes = append(indexes, idx)
	}
	return indexes, nil
}

// ─── type mapping helpers ─────────────────────────────────────────────────────

var (
	reAutoincrement = regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`)
	reTypeLength    = regexp.MustCompile(`\((\d+)\)`)
)

// normalizeDeclaredType lowercases the declared column type for the snapshot.
// SQLite allows any (or no) declared type; an empty declaration has BLOB
// affinity and is reported as "blob".
func normalizeDeclaredType(declType string) string {
	t := strings.ToLower(strings.TrimSpace(declType))
	if t == "" {
		return "blob"
	}
	return t
}

// mapDeclaredTypeToGo maps a normalized declared type to a Go type using
// SQLite's type-affinity rules, refined by the framework's column-name
// conventions:
//
//   - TEXT-affinity columns named *_at / *_date are timestamps stored as text
//     and map to time.Time.
//   - INTEGER-affinity columns named is_* / has_* / can_* / was_* / *_flag
//     store 0/1 booleans and map to bool.
//   - uuid-ish TEXT columns stay string (no special casing needed).
func mapDeclaredTypeToGo(dbType, colName string, nullable bool) (goType, importPath string) {
	upper := strings.ToUpper(dbType)
	lower := strings.ToLower(colName)

	isBoolName := strings.HasPrefix(lower, "is_") || strings.HasPrefix(lower, "has_") ||
		strings.HasPrefix(lower, "can_") || strings.HasPrefix(lower, "was_") ||
		strings.HasSuffix(lower, "_flag")
	isTimeName := strings.HasSuffix(lower, "_at") || strings.HasSuffix(lower, "_date")

	var gt, imp string
	switch {
	// Declared-type keywords first: these would otherwise fall into the
	// INTEGER/NUMERIC affinity buckets below.
	case strings.Contains(upper, "BOOL"):
		gt = "bool"
	case strings.Contains(upper, "DATE"), strings.Contains(upper, "TIME"):
		gt, imp = "time.Time", "time"

	// Affinity rule 1: INT anywhere in the declared type → INTEGER affinity.
	case strings.Contains(upper, "BIGINT"):
		gt = "int64"
	case strings.Contains(upper, "SMALLINT"), strings.Contains(upper, "TINYINT"):
		gt = "int16"
	case strings.Contains(upper, "INT"):
		// SQLite integers are up to 8 bytes (and rowid PKs are 64-bit).
		gt = "int64"

	// Affinity rule 2: CHAR/CLOB/TEXT → TEXT affinity.
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		gt = "string"

	// Affinity rule 3: BLOB → BLOB affinity.
	case strings.Contains(upper, "BLOB"):
		gt = "[]byte"

	// Affinity rule 4: REAL/FLOA/DOUB → REAL affinity (always 8-byte floats).
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		gt = "float64"

	case strings.Contains(upper, "DEC"), strings.Contains(upper, "NUMERIC"):
		gt = "float64"

	// Affinity rule 5: everything else (uuid, json, custom types) has
	// NUMERIC affinity but is overwhelmingly stored as text.
	default:
		gt = "string"
	}

	// Column-name refinement.
	switch {
	case gt == "string" && isTimeName:
		gt, imp = "time.Time", "time"
	case (gt == "int64" || gt == "int16") && isBoolName:
		gt, imp = "bool", ""
	}

	if nullable && !strings.HasPrefix(gt, "[]") {
		return "*" + gt, imp
	}
	return gt, imp
}

// parseMaxLength extracts N from declared types like "varchar(255)".
// Only meaningful for CHAR-family types; SQLite does not enforce it.
func parseMaxLength(dbType string) int {
	if !strings.Contains(strings.ToUpper(dbType), "CHAR") {
		return 0
	}
	m := reTypeLength.FindStringSubmatch(dbType)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// normalizeFKAction converts foreign_key_list action spellings ("NO ACTION",
// "SET NULL", …) to the snapshot's underscore form ("NO_ACTION", "SET_NULL").
func normalizeFKAction(action string) string {
	a := strings.ToUpper(strings.TrimSpace(action))
	if a == "" {
		return "NO_ACTION"
	}
	return strings.ReplaceAll(a, " ", "_")
}

// cleanDefault strips surrounding quotes from a literal default value so
// "'active'" is recorded as "active", matching the Postgres reflector.
// Expression defaults like (datetime('now')) or CURRENT_TIMESTAMP pass
// through unchanged.
func cleanDefault(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

// deriveValidationTags mirrors the Postgres reflector's tag derivation.
func deriveValidationTags(col schema.ColumnInfo) string {
	var tags []string
	if !col.IsNullable && !col.IsPrimaryKey {
		tags = append(tags, "required")
	}
	if col.DBType == "uuid" {
		tags = append(tags, "uuid")
	}
	if col.MaxLength > 0 {
		tags = append(tags, fmt.Sprintf("max=%d", col.MaxLength))
	}
	if strings.Contains(strings.ToLower(col.Name), "email") {
		tags = append(tags, "email")
	}
	return strings.Join(tags, ",")
}
