package pgx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// store is the low-level Postgres query interface for schema reflection.
type store struct {
	pool   *pgxpool.Pool
	dbName string
	ctx    context.Context
}

func newStore(ctx context.Context, pool *pgxpool.Pool, dbName string) *store {
	return &store{pool: pool, dbName: dbName, ctx: ctx}
}

func (s *store) getSourceType() string   { return "postgres" }
func (s *store) getDatabaseName() string { return s.dbName }

// reflectSchema orchestrates schema reflection and returns a ReflectedSchema.
func reflectSchema(ctx context.Context, pool *pgxpool.Pool, dbName, schemaName string) (*schema.ReflectedSchema, error) {
	st := newStore(ctx, pool, dbName)

	result := &schema.ReflectedSchema{
		Version:     "1.0",
		Source:      st.getSourceType(),
		Database:    st.getDatabaseName(),
		SchemaName:  schemaName,
		ReflectedAt: time.Now(),
		Tables:      make(map[string]*schema.TableInfo),
		EnumTypes:   make(map[string]*schema.EnumTypeInfo),
	}

	enumTypes, err := st.getEnumTypes(schemaName)
	if err != nil {
		fmt.Printf("Warning: could not get enum types for %s: %v\n", schemaName, err)
	} else {
		for _, et := range enumTypes {
			e := et
			result.EnumTypes[et.Name] = &e
		}
	}

	tables, err := st.getTables(schemaName)
	if err != nil {
		return nil, fmt.Errorf("get tables: %w", err)
	}

	for _, tableName := range tables {
		table := &schema.TableInfo{
			TableName:   tableName,
			Schema:      schemaName,
			Columns:     []schema.ColumnInfo{},
			ForeignKeys: []schema.ForeignKeyInfo{},
			Indexes:     []schema.IndexInfo{},
			Constraints: []schema.ConstraintInfo{},
		}

		columns, err := st.getColumns(schemaName, tableName)
		if err != nil {
			return nil, fmt.Errorf("get columns for %s: %w", tableName, err)
		}
		table.Columns = columns

		pk, err := st.getPrimaryKey(schemaName, tableName, columns)
		if err != nil {
			fmt.Printf("Warning: could not get primary key for %s.%s: %v\n", schemaName, tableName, err)
		} else {
			table.PrimaryKey = pk
		}

		if pk != nil {
			pkCols := make(map[string]bool)
			for _, c := range pk.Columns {
				pkCols[c] = true
			}
			if len(pk.Columns) == 0 && pk.Column != "" {
				pkCols[pk.Column] = true
			}
			for i := range table.Columns {
				if pkCols[table.Columns[i].Name] {
					table.Columns[i].IsPrimaryKey = true
				}
			}
		}

		fks, err := st.getForeignKeys(schemaName, tableName)
		if err != nil {
			return nil, fmt.Errorf("get foreign keys for %s: %w", tableName, err)
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

		indexes, err := st.getIndexes(schemaName, tableName)
		if err != nil {
			return nil, fmt.Errorf("get indexes for %s: %w", tableName, err)
		}
		table.Indexes = indexes

		constraints, err := st.getConstraints(schemaName, tableName)
		if err != nil {
			return nil, fmt.Errorf("get constraints for %s: %w", tableName, err)
		}
		table.Constraints = constraints

		uniqueCols := make(map[string]bool)
		for _, c := range constraints {
			if c.Type == "UNIQUE" && len(c.Columns) == 1 {
				uniqueCols[c.Columns[0]] = true
			}
		}
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

		comment, _ := st.getTableComment(schemaName, tableName)
		table.Comment = comment

		result.Tables[tableName] = table
	}

	return result, nil
}

// ─── store query methods ──────────────────────────────────────────────────────

func (s *store) getTables(schemaName string) ([]string, error) {
	query := `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = $1
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`
	rows, err := s.pool.Query(s.ctx, query, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func (s *store) getColumns(schemaName, tableName string) ([]schema.ColumnInfo, error) {
	query := `
		SELECT
			c.column_name,
			c.data_type,
			c.udt_name,
			c.is_nullable,
			c.column_default,
			c.character_maximum_length,
			c.numeric_precision,
			c.numeric_scale,
			pgd.description,
			COALESCE(pt.typtype = 'e', false) AS is_enum,
			CASE WHEN pt.typtype = 'e' THEN
				ARRAY(SELECT pe.enumlabel FROM pg_enum pe WHERE pe.enumtypid = pt.oid ORDER BY pe.enumsortorder)
			ELSE NULL END AS enum_values,
			COALESCE(pa.attidentity::text, '') AS identity_type
		FROM information_schema.columns c
		LEFT JOIN pg_catalog.pg_statio_all_tables pst
			ON c.table_schema = pst.schemaname AND c.table_name = pst.relname
		LEFT JOIN pg_catalog.pg_description pgd
			ON pgd.objoid = pst.relid AND pgd.objsubid = c.ordinal_position
		LEFT JOIN pg_catalog.pg_type pt
			ON pt.typname = c.udt_name
			AND pt.typnamespace = (SELECT oid FROM pg_namespace WHERE nspname = c.udt_schema)
		LEFT JOIN pg_catalog.pg_attribute pa
			ON pa.attrelid = pst.relid AND pa.attname = c.column_name
		WHERE c.table_schema = $1
		  AND c.table_name = $2
		ORDER BY c.ordinal_position
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []schema.ColumnInfo
	for rows.Next() {
		var col schema.ColumnInfo
		var dataType, udtName, isNullable, identityType string
		var defaultValue, comment *string
		var maxLength, precision, scale *int64
		var isEnum bool
		var enumValues []string

		if err := rows.Scan(
			&col.Name, &dataType, &udtName, &isNullable,
			&defaultValue, &maxLength, &precision, &scale,
			&comment, &isEnum, &enumValues, &identityType,
		); err != nil {
			return nil, err
		}

		col.DBType = normalizeType(dataType, udtName, maxLength, precision, scale)
		col.IsNullable = isNullable == "YES"

		if defaultValue != nil {
			col.HasDefault = true
			col.DefaultValue = cleanDefault(*defaultValue)
			if strings.Contains(*defaultValue, "nextval") {
				col.IsAutoIncrement = true
				if strings.Contains(col.DBType, "bigint") || strings.Contains(col.DBType, "int8") {
					col.AutoIncrementType = "BIGSERIAL"
				} else {
					col.AutoIncrementType = "SERIAL"
				}
			}
		}

		if identityType != "" {
			col.IsAutoIncrement = true
			col.AutoIncrementType = "IDENTITY"
		}

		if isEnum {
			col.IsEnum = true
			col.EnumType = udtName
			col.EnumValues = enumValues
		}

		if maxLength != nil {
			col.MaxLength = int(*maxLength)
		}
		if precision != nil {
			col.Precision = int(*precision)
		}
		if scale != nil {
			col.Scale = int(*scale)
		}
		if comment != nil {
			col.Comment = *comment
		}

		if col.IsEnum {
			col.GoType = "string"
			if col.IsNullable {
				col.GoType = "*string"
			}
		} else {
			col.GoType, col.GoImport = mapTypeToGo(col.DBType, col.IsNullable)
		}
		col.ValidationTags = deriveValidationTags(col)

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (s *store) getPrimaryKey(schemaName, tableName string, columns []schema.ColumnInfo) (*schema.PrimaryKeyInfo, error) {
	query := `
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = $1
		  AND tc.table_name = $2
		ORDER BY kcu.ordinal_position
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName, tableName)
	if err != nil {
		return nil, fmt.Errorf("query primary key: %w", err)
	}
	defer rows.Close()

	var pkCols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		pkCols = append(pkCols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(pkCols) == 0 {
		return nil, fmt.Errorf("no primary key found")
	}

	var firstCol *schema.ColumnInfo
	for i := range columns {
		if columns[i].Name == pkCols[0] {
			firstCol = &columns[i]
			break
		}
	}
	if firstCol == nil {
		return nil, fmt.Errorf("primary key column %s not found in columns", pkCols[0])
	}

	return &schema.PrimaryKeyInfo{
		Column:      pkCols[0],
		Columns:     pkCols,
		DBType:      firstCol.DBType,
		GoType:      firstCol.GoType,
		HasDefault:  firstCol.HasDefault,
		DefaultExpr: firstCol.DefaultValue,
	}, nil
}

func (s *store) getForeignKeys(schemaName, tableName string) ([]schema.ForeignKeyInfo, error) {
	// Use pg_constraint directly to avoid the cartesian product bug that
	// information_schema.constraint_column_usage produces for composite FKs.
	query := `
		SELECT
			con.conname,
			ARRAY(
				SELECT att.attname
				FROM unnest(con.conkey) WITH ORDINALITY AS col(num, ord)
				JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = col.num
				ORDER BY col.ord
			) AS columns,
			ref_nsp.nspname,
			ref_cls.relname,
			ARRAY(
				SELECT att.attname
				FROM unnest(con.confkey) WITH ORDINALITY AS col(num, ord)
				JOIN pg_attribute att ON att.attrelid = con.confrelid AND att.attnum = col.num
				ORDER BY col.ord
			) AS ref_columns,
			CASE con.confupdtype
				WHEN 'a' THEN 'NO_ACTION' WHEN 'r' THEN 'RESTRICT'
				WHEN 'c' THEN 'CASCADE'   WHEN 'n' THEN 'SET_NULL'
				WHEN 'd' THEN 'SET_DEFAULT' ELSE 'NO_ACTION'
			END,
			CASE con.confdeltype
				WHEN 'a' THEN 'NO_ACTION' WHEN 'r' THEN 'RESTRICT'
				WHEN 'c' THEN 'CASCADE'   WHEN 'n' THEN 'SET_NULL'
				WHEN 'd' THEN 'SET_DEFAULT' ELSE 'NO_ACTION'
			END
		FROM pg_constraint con
		JOIN pg_namespace nsp ON nsp.oid = con.connamespace
		JOIN pg_class cls ON cls.oid = con.conrelid
		JOIN pg_class ref_cls ON ref_cls.oid = con.confrelid
		JOIN pg_namespace ref_nsp ON ref_nsp.oid = ref_cls.relnamespace
		WHERE con.contype = 'f'
		  AND nsp.nspname = $1
		  AND cls.relname = $2
		ORDER BY con.conname
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []schema.ForeignKeyInfo
	for rows.Next() {
		var fk schema.ForeignKeyInfo
		var cols, refCols []string
		if err := rows.Scan(&fk.ConstraintName, &cols, &fk.RefSchema, &fk.RefTable, &refCols, &fk.OnUpdate, &fk.OnDelete); err != nil {
			return nil, err
		}
		fk.Columns = cols
		fk.RefColumns = refCols
		if len(cols) > 0 {
			fk.ColumnName = cols[0]
		}
		if len(refCols) > 0 {
			fk.RefColumn = refCols[0]
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

func (s *store) getIndexes(schemaName, tableName string) ([]schema.IndexInfo, error) {
	// Use pg_get_indexdef for expression columns (attnum=0) and pg_get_expr
	// for partial index WHERE predicates.
	query := `
		SELECT
			i.relname,
			am.amname,
			ix.indisunique,
			pg_get_indexdef(ix.indexrelid),
			COALESCE(pg_get_expr(ix.indpred, ix.indrelid), ''),
			ARRAY(
				SELECT CASE
					WHEN col_num = 0 THEN
						pg_get_indexdef(ix.indexrelid, ord::int, true)
					ELSE
						(SELECT att.attname FROM pg_attribute att
						 WHERE att.attrelid = t.oid AND att.attnum = col_num)
				END
				FROM unnest(ix.indkey) WITH ORDINALITY AS col(col_num, ord)
			)
		FROM pg_class t
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_am am ON i.relam = am.oid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = $1
		  AND t.relname = $2
		  AND NOT ix.indisprimary
		ORDER BY i.relname
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []schema.IndexInfo
	for rows.Next() {
		var idx schema.IndexInfo
		var cols []string
		if err := rows.Scan(&idx.Name, &idx.Method, &idx.Unique, &idx.Definition, &idx.Predicate, &cols); err != nil {
			return nil, err
		}
		idx.Columns = cols
		indexes = append(indexes, idx)
	}
	return indexes, rows.Err()
}

func (s *store) getConstraints(schemaName, tableName string) ([]schema.ConstraintInfo, error) {
	query := `
		SELECT
			con.conname,
			CASE con.contype
				WHEN 'c' THEN 'CHECK'
				WHEN 'u' THEN 'UNIQUE'
				WHEN 'x' THEN 'EXCLUDE'
			END,
			pg_get_constraintdef(con.oid),
			COALESCE(
				ARRAY(
					SELECT att.attname
					FROM unnest(con.conkey) AS col_num
					JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = col_num
					ORDER BY array_position(con.conkey, col_num)
				),
				ARRAY[]::text[]
			)
		FROM pg_constraint con
		JOIN pg_namespace nsp ON nsp.oid = con.connamespace
		JOIN pg_class cls ON cls.oid = con.conrelid
		WHERE nsp.nspname = $1
		  AND cls.relname = $2
		  AND con.contype IN ('c', 'u', 'x')
		ORDER BY con.conname
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var constraints []schema.ConstraintInfo
	for rows.Next() {
		var c schema.ConstraintInfo
		var cols []string
		if err := rows.Scan(&c.Name, &c.Type, &c.Definition, &cols); err != nil {
			return nil, err
		}
		c.Columns = cols
		constraints = append(constraints, c)
	}
	return constraints, rows.Err()
}

func (s *store) getEnumTypes(schemaName string) ([]schema.EnumTypeInfo, error) {
	query := `
		SELECT
			t.typname,
			n.nspname,
			ARRAY(
				SELECT e.enumlabel FROM pg_enum e WHERE e.enumtypid = t.oid ORDER BY e.enumsortorder
			)
		FROM pg_type t
		JOIN pg_namespace n ON t.typnamespace = n.oid
		WHERE t.typtype = 'e'
		  AND n.nspname = $1
		ORDER BY t.typname
	`

	rows, err := s.pool.Query(s.ctx, query, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var enums []schema.EnumTypeInfo
	for rows.Next() {
		var e schema.EnumTypeInfo
		if err := rows.Scan(&e.Name, &e.Schema, &e.Values); err != nil {
			return nil, err
		}
		enums = append(enums, e)
	}
	return enums, rows.Err()
}

func (s *store) getTableComment(schemaName, tableName string) (string, error) {
	query := `
		SELECT pg_catalog.obj_description(c.oid, 'pg_class')
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2
	`
	var comment *string
	err := s.pool.QueryRow(s.ctx, query, schemaName, tableName).Scan(&comment)
	if err != nil {
		return "", err
	}
	if comment != nil {
		return *comment, nil
	}
	return "", nil
}

// ─── type mapping helpers ─────────────────────────────────────────────────────

var castRe = regexp.MustCompile(`::[\w\s]+(\[\])?`)

func normalizeType(dataType, udtName string, maxLen, prec, scale *int64) string {
	base := udtName
	switch base {
	case "varchar", "character varying":
		if maxLen != nil && *maxLen > 0 {
			return fmt.Sprintf("varchar(%d)", *maxLen)
		}
		return "varchar"
	case "bpchar":
		if maxLen != nil && *maxLen > 0 {
			return fmt.Sprintf("char(%d)", *maxLen)
		}
		return "char"
	case "numeric", "decimal":
		if prec != nil && scale != nil && *prec > 0 && *scale > 0 {
			return fmt.Sprintf("numeric(%d,%d)", *prec, *scale)
		}
		if prec != nil && *prec > 0 {
			return fmt.Sprintf("numeric(%d)", *prec)
		}
		return "numeric"
	case "_text":
		return "text[]"
	case "_varchar":
		return "varchar[]"
	case "_int4":
		return "integer[]"
	default:
		return base
	}
}

func mapTypeToGo(dbType string, nullable bool) (goType, importPath string) {
	base := dbType
	if idx := strings.Index(dbType, "("); idx != -1 {
		base = strings.TrimSpace(dbType[:idx])
	}

	var gt, imp string
	switch base {
	case "uuid":
		gt = "string"
	case "text", "varchar", "character varying", "char", "bpchar":
		gt = "string"
	case "integer", "int", "int4":
		gt = "int"
	case "smallint", "int2":
		gt = "int16"
	case "bigint", "int8":
		gt = "int64"
	case "real", "float4":
		gt = "float32"
	case "double precision", "float8":
		gt = "float64"
	case "numeric", "decimal":
		gt = "float64"
	case "boolean", "bool":
		gt = "bool"
	case "timestamp", "timestamptz", "date", "time":
		gt = "time.Time"
		imp = "time"
	case "json", "jsonb":
		gt = "json.RawMessage"
		imp = "encoding/json"
	case "text[]", "varchar[]":
		return "[]string", ""
	case "integer[]":
		return "[]int", ""
	case "bytea":
		return "[]byte", ""
	case "inet":
		gt = "string"
	default:
		gt = "interface{}"
	}

	if nullable && !strings.HasPrefix(gt, "[]") && !strings.HasPrefix(gt, "*") {
		return "*" + gt, imp
	}
	return gt, imp
}

func cleanDefault(s string) string {
	s = castRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "'")
	return s
}

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
