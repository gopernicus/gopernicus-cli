package scaffold

import (
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

func TestDetectAncestry(t *testing.T) {
	t.Run("no ancestry", func(t *testing.T) {
		table := &schema.TableInfo{
			TableName:  "sessions",
			PrimaryKey: &schema.PrimaryKeyInfo{Column: "session_id"},
			ForeignKeys: []schema.ForeignKeyInfo{
				{ColumnName: "user_id", Columns: []string{"user_id"}, RefTable: "users"},
			},
		}
		anc := DetectAncestry(table)
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
		anc := DetectAncestry(table)
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
		anc := DetectAncestry(table)
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
		anc := DetectAncestry(table)
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
		anc := DetectAncestry(table)
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
