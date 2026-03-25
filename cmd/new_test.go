package cmd

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

	got := scaffoldQueries(table, "primary", "users", "user", ancestry{})
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

	got := scaffoldQueries(table, "primary", "widgets", "widget", ancestry{})

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

func TestDetectAncestry(t *testing.T) {
	t.Run("no ancestry", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "sessions",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "session_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "user_id", Columns: []string{"user_id"}, RefTable: "users"},
			},
		}
		anc := detectAncestry(table)
		if anc.Tenant != nil {
			t.Errorf("expected nil Tenant, got %+v", anc.Tenant)
		}
		if anc.Parent != nil {
			t.Errorf("expected nil Parent, got %+v", anc.Parent)
		}
	})

	t.Run("tenant only", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "questions",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "question_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "tenant_id", Columns: []string{"tenant_id"}, RefTable: "tenants"},
			},
		}
		anc := detectAncestry(table)
		if anc.Tenant == nil {
			t.Fatal("expected Tenant")
		}
		if anc.Tenant.Column != "tenant_id" {
			t.Errorf("Tenant.Column = %q", anc.Tenant.Column)
		}
		if anc.Parent != nil {
			t.Errorf("expected nil Parent, got %+v", anc.Parent)
		}
	})

	t.Run("tenant + parent", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "takes",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "take_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "tenant_id", Columns: []string{"tenant_id"}, RefTable: "tenants"},
				{ColumnName: "parent_question_id", Columns: []string{"parent_question_id"}, RefTable: "questions"},
			},
		}
		anc := detectAncestry(table)
		if anc.Tenant == nil {
			t.Fatal("expected Tenant")
		}
		if anc.Tenant.Column != "tenant_id" {
			t.Errorf("Tenant.Column = %q", anc.Tenant.Column)
		}
		if anc.Parent == nil {
			t.Fatal("expected Parent")
		}
		if anc.Parent.Column != "parent_question_id" {
			t.Errorf("Parent.Column = %q, want parent_question_id", anc.Parent.Column)
		}
		if anc.Parent.RelName != "question" {
			t.Errorf("Parent.RelName = %q, want question", anc.Parent.RelName)
		}
	})

	t.Run("parent only (no tenant)", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "api_keys",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "api_key_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "parent_service_account_id", Columns: []string{"parent_service_account_id"}, RefTable: "service_accounts"},
			},
		}
		anc := detectAncestry(table)
		if anc.Tenant != nil {
			t.Errorf("expected nil Tenant, got %+v", anc.Tenant)
		}
		if anc.Parent == nil {
			t.Fatal("expected Parent")
		}
		if anc.Parent.Column != "parent_service_account_id" {
			t.Errorf("Parent.Column = %q", anc.Parent.Column)
		}
	})

	t.Run("parent_tenant_id treated as tenant", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "projects",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "project_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "parent_tenant_id", Columns: []string{"parent_tenant_id"}, RefTable: "tenants"},
			},
		}
		anc := detectAncestry(table)
		if anc.Tenant == nil {
			t.Fatal("expected Tenant (from parent_tenant_id)")
		}
		if anc.Tenant.Column != "parent_tenant_id" {
			t.Errorf("Tenant.Column = %q, want parent_tenant_id", anc.Tenant.Column)
		}
		if anc.Parent != nil {
			t.Errorf("expected nil Parent (parent_tenant_id is tenant, not generic parent)")
		}
	})
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
	anc := detectAncestry(table)
	got := scaffoldQueries(table, "primary", "projects", "project", anc)

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
	got := scaffoldQueries(table, "primary", "users", "user", ancestry{})

	for _, s := range []string{"@auth.relation:", "@auth.permission:"} {
		if strings.Contains(got, s) {
			t.Errorf("queries.sql should not contain %q", s)
		}
	}
}
