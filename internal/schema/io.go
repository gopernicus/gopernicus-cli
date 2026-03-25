package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// WriteJSON writes the schema to a JSON file at the given path.
func WriteJSON(s *ReflectedSchema, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// LoadJSON reads a reflected schema from a JSON file.
func LoadJSON(path string) (*ReflectedSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s ReflectedSchema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WriteSQL writes a human-readable SQL summary of the schema.
func WriteSQL(s *ReflectedSchema, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "-- Schema: %s.%s  reflected at: %s\n\n",
		s.Database, s.SchemaName, s.ReflectedAt.Format("2006-01-02 15:04:05"))

	// Enum types
	if len(s.EnumTypes) > 0 {
		enumNames := make([]string, 0, len(s.EnumTypes))
		for n := range s.EnumTypes {
			enumNames = append(enumNames, n)
		}
		sort.Strings(enumNames)

		for _, name := range enumNames {
			et := s.EnumTypes[name]
			quoted := make([]string, len(et.Values))
			for i, v := range et.Values {
				quoted[i] = "'" + v + "'"
			}
			fmt.Fprintf(f, "CREATE TYPE %s.%s AS ENUM (%s);\n", et.Schema, et.Name, strings.Join(quoted, ", "))
		}
		fmt.Fprintln(f)
	}

	// Tables
	names := make([]string, 0, len(s.Tables))
	for n := range s.Tables {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		table := s.Tables[name]
		if table.Comment != "" {
			fmt.Fprintf(f, "-- %s\n", table.Comment)
		}
		fmt.Fprintf(f, "CREATE TABLE %s.%s (\n", table.Schema, table.TableName)

		var lines []string
		for _, col := range table.Columns {
			line := fmt.Sprintf("    %s %s", col.Name, col.DBType)
			if !col.IsNullable {
				line += " NOT NULL"
			}
			if col.HasDefault && col.DefaultValue != "" {
				line += fmt.Sprintf(" DEFAULT %s", col.DefaultValue)
			}
			lines = append(lines, line)
		}

		// Primary key
		if table.PrimaryKey != nil {
			cols := table.PrimaryKey.Columns
			if len(cols) == 0 && table.PrimaryKey.Column != "" {
				cols = []string{table.PrimaryKey.Column}
			}
			lines = append(lines, fmt.Sprintf("    PRIMARY KEY (%s)", strings.Join(cols, ", ")))
		}

		// Constraints (CHECK, UNIQUE, EXCLUDE)
		for _, c := range table.Constraints {
			lines = append(lines, fmt.Sprintf("    CONSTRAINT %s %s", c.Name, c.Definition))
		}

		fmt.Fprintln(f, strings.Join(lines, ",\n"))
		fmt.Fprintln(f, ");")

		// Foreign keys
		for _, fk := range table.ForeignKeys {
			cols := fk.Columns
			refCols := fk.RefColumns
			if len(cols) == 0 {
				cols = []string{fk.ColumnName}
			}
			if len(refCols) == 0 {
				refCols = []string{fk.RefColumn}
			}
			fmt.Fprintf(f, "ALTER TABLE %s.%s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.%s(%s)",
				table.Schema, table.TableName, fk.ConstraintName,
				strings.Join(cols, ", "),
				fk.RefSchema, fk.RefTable,
				strings.Join(refCols, ", "))
			if fk.OnDelete != "" && fk.OnDelete != "NO_ACTION" {
				fmt.Fprintf(f, " ON DELETE %s", strings.ReplaceAll(fk.OnDelete, "_", " "))
			}
			if fk.OnUpdate != "" && fk.OnUpdate != "NO_ACTION" {
				fmt.Fprintf(f, " ON UPDATE %s", strings.ReplaceAll(fk.OnUpdate, "_", " "))
			}
			fmt.Fprintln(f, ";")
		}

		// Indexes
		for _, idx := range table.Indexes {
			if idx.Definition != "" {
				fmt.Fprintf(f, "%s;\n", idx.Definition)
			} else {
				unique := ""
				if idx.Unique {
					unique = "UNIQUE "
				}
				using := ""
				if idx.Method != "" && idx.Method != "btree" {
					using = fmt.Sprintf(" USING %s", idx.Method)
				}
				predicate := ""
				if idx.Predicate != "" {
					predicate = fmt.Sprintf(" WHERE %s", idx.Predicate)
				}
				fmt.Fprintf(f, "CREATE %sINDEX %s ON %s.%s%s (%s)%s;\n",
					unique, idx.Name, table.Schema, table.TableName, using,
					strings.Join(idx.Columns, ", "), predicate)
			}
		}

		fmt.Fprintln(f)
	}
	return nil
}
