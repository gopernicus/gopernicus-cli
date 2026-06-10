package scaffold

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

func TestScaffoldQueries_Users(t *testing.T) {
	table := &schema.TableInfo{
		TableName: "users",
		Schema:    "public",
		PrimaryKey: &schema.PrimaryKeyInfo{
			Column: "user_id", Columns: []string{"user_id"}, DBType: "varchar", GoType: "string",
		},
		Columns: []schema.ColumnInfo{
			{Name: "user_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true, IsForeignKey: true},
			{Name: "email", DBType: "varchar(255)", GoType: "string", IsUnique: true},
			{Name: "display_name", DBType: "varchar(255)", GoType: "*string", IsNullable: true},
			{Name: "record_state", DBType: "varchar(50)", GoType: "string", HasDefault: true, DefaultValue: "active"},
			{Name: "created_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time", HasDefault: true},
			{Name: "updated_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time", HasDefault: true},
		},
	}

	got := Queries(table, "users", "user", Ancestry{})
	fmt.Println(got)

	checks := map[string]string{
		"filter":       "@filter:conditions *",
		"order":        "@order: *",
		"max":          "@max: 100",
		"search":       "@search: ilike(",
		"soft delete":  "record_state = 'deleted'",
		"SoftDelete":   "-- @func: SoftDelete",
		"hard delete":  "DELETE FROM users",
		"create fields": "-- @fields: *,-created_at,-updated_at",
		"update fields": "-- @fields: *,-user_id,-record_state,-created_at",
	}
	for desc, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: want %q", desc, want)
		}
	}

	// No protocol or auth annotations.
	for _, s := range []string{"@http:json", "@authenticated", "@authorize:", "@auth.create:", "@auth.relation:", "@auth.permission:"} {
		if strings.Contains(got, s) {
			t.Errorf("should not contain %q (lives in bridge.yml)", s)
		}
	}
}

func TestScaffoldQueries_NoSoftDelete(t *testing.T) {
	table := &schema.TableInfo{
		TableName: "widgets",
		Schema:    "public",
		PrimaryKey: &schema.PrimaryKeyInfo{
			Column: "widget_id", Columns: []string{"widget_id"}, DBType: "uuid", GoType: "string",
		},
		Columns: []schema.ColumnInfo{
			{Name: "widget_id", DBType: "uuid", GoType: "string", IsPrimaryKey: true, HasDefault: true},
			{Name: "name", DBType: "varchar(255)", GoType: "string"},
			{Name: "description", DBType: "text", GoType: "*string", IsNullable: true},
			{Name: "created_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time", HasDefault: true},
		},
	}

	got := Queries(table, "widgets", "widget", Ancestry{})

	if strings.Contains(got, "record_state = 'deleted'") {
		t.Error("should not have soft delete")
	}
	if !strings.Contains(got, "DELETE FROM widgets") {
		t.Error("missing hard delete")
	}
	if !strings.Contains(got, "ilike(name, description)") {
		t.Error("missing search fields")
	}
	for _, s := range []string{"@http:json", "@authenticated", "@authorize:", "@auth.relation:"} {
		if strings.Contains(got, s) {
			t.Errorf("should not contain %q", s)
		}
	}
}

func TestScaffoldQueries_TenantScoped(t *testing.T) {
	table := &schema.TableInfo{
		TableName: "projects",
		Schema:    "public",
		PrimaryKey: &schema.PrimaryKeyInfo{Column: "project_id", DBType: "varchar", GoType: "string"},
		Columns: []schema.ColumnInfo{
			{Name: "project_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "parent_tenant_id", DBType: "varchar", GoType: "string", IsForeignKey: true},
			{Name: "name", DBType: "varchar(255)", GoType: "string"},
			{Name: "description", DBType: "text", GoType: "*string", IsNullable: true},
			{Name: "record_state", DBType: "varchar(50)", GoType: "string", HasDefault: true},
			{Name: "created_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time", HasDefault: true},
			{Name: "updated_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time", HasDefault: true},
		},
		ForeignKeys: []schema.ForeignKeyInfo{
			{ColumnName: "parent_tenant_id", Columns: []string{"parent_tenant_id"}, RefTable: "tenants"},
		},
	}
	anc := DetectAncestry(table)
	got := Queries(table, "projects", "project", anc)

	checks := map[string]string{
		"list tenant where":   "WHERE parent_tenant_id = @parent_tenant_id AND $conditions",
		"get tenant where":    "WHERE project_id = @project_id AND parent_tenant_id = @parent_tenant_id",
		"delete tenant where": "WHERE project_id = @project_id AND parent_tenant_id = @parent_tenant_id",
		"update excludes":     "@fields: *,-project_id,-parent_tenant_id,-record_state,-created_at",
	}
	for desc, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: want %q\n\ngot:\n%s", desc, want, got)
		}
	}

	for _, s := range []string{"@http:json", "@authenticated", "@authorize:", "@auth.create:", "@auth.relation:", "@auth.permission:"} {
		if strings.Contains(got, s) {
			t.Errorf("should not contain %q", s)
		}
	}
}

func TestScaffoldQueries_NoAuthAnnotations(t *testing.T) {
	table := &schema.TableInfo{TableName: "users"}
	got := Queries(table, "users", "user", Ancestry{})

	for _, s := range []string{"@auth.relation:", "@auth.permission:"} {
		if strings.Contains(got, s) {
			t.Errorf("queries.sql should not contain %q", s)
		}
	}
}
