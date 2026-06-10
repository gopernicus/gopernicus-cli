package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// usersFixtureSQL is a users-like schema exercising the snapshot surface:
// text PK, nullable TEXT timestamp, unique email, defaults, secondary index,
// composite PK, single-column FK with action, implicit-PK FK, composite FK,
// and an INTEGER PRIMARY KEY rowid alias.
const usersFixtureSQL = `
CREATE TABLE users (
	user_id      TEXT PRIMARY KEY,
	email        TEXT NOT NULL UNIQUE,
	display_name VARCHAR(255),
	is_admin     INTEGER NOT NULL DEFAULT 0,
	login_count  INTEGER NOT NULL DEFAULT 0,
	score        REAL,
	avatar       BLOB,
	record_state TEXT NOT NULL DEFAULT 'active',
	created_at   TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
	last_login_at TEXT
);
CREATE INDEX idx_users_record_state ON users(record_state);

CREATE TABLE orgs (
	org_id TEXT PRIMARY KEY,
	name   TEXT NOT NULL
);

CREATE TABLE memberships (
	org_id  TEXT NOT NULL REFERENCES orgs,
	user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
	role    TEXT NOT NULL DEFAULT 'member',
	PRIMARY KEY (org_id, user_id)
);

CREATE TABLE membership_notes (
	note_id INTEGER PRIMARY KEY,
	org_id  TEXT NOT NULL,
	user_id TEXT NOT NULL,
	body    TEXT NOT NULL,
	FOREIGN KEY (org_id, user_id) REFERENCES memberships(org_id, user_id) ON DELETE CASCADE
);
`

// reflectFixture creates a temp sqlite database with the fixture schema and
// reflects it.
func reflectFixture(t *testing.T) *schema.ReflectedSchema {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "app.db")
	d := openFixture(t, ctx, path)
	defer d.Close()

	s, err := d.Reflect(ctx, "public")
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	return s
}

// openFixture creates the database file with the fixture schema and opens a
// Driver on it.
func openFixture(t *testing.T, ctx context.Context, path string) *Driver {
	t.Helper()

	// Create via the file: URI form so New's existence check is bypassed for
	// the initial bootstrap.
	boot, err := New(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("New(bootstrap): %v", err)
	}
	if _, err := boot.db.ExecContext(ctx, usersFixtureSQL); err != nil {
		boot.Close()
		t.Fatalf("creating fixture schema: %v", err)
	}
	boot.Close()

	d, err := New(ctx, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func columnByName(t *testing.T, table *schema.TableInfo, name string) schema.ColumnInfo {
	t.Helper()
	for _, c := range table.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("column %q not found in table %s", name, table.TableName)
	return schema.ColumnInfo{}
}

func TestReflectSnapshotShape(t *testing.T) {
	s := reflectFixture(t)

	if s.Source != "sqlite" {
		t.Errorf("Source = %q, want %q", s.Source, "sqlite")
	}
	if s.SchemaName != "public" {
		t.Errorf("SchemaName = %q, want %q (stamped with the requested name)", s.SchemaName, "public")
	}
	if s.Database != "app" {
		t.Errorf("Database = %q, want %q", s.Database, "app")
	}
	if len(s.EnumTypes) != 0 {
		t.Errorf("EnumTypes = %d entries, want 0 (sqlite has no enums)", len(s.EnumTypes))
	}
	if len(s.Tables) != 4 {
		t.Errorf("Tables = %d, want 4", len(s.Tables))
	}
}

func TestReflectUsersColumns(t *testing.T) {
	s := reflectFixture(t)
	users := s.Tables["users"]
	if users == nil {
		t.Fatal("users table not reflected")
	}

	cases := []struct {
		name       string
		dbType     string
		goType     string
		goImport   string
		nullable   bool
		hasDefault bool
		defaultVal string
	}{
		{"user_id", "text", "string", "", false, false, ""},
		{"email", "text", "string", "", false, false, ""},
		{"display_name", "varchar(255)", "*string", "", true, false, ""},
		{"is_admin", "integer", "bool", "", false, true, "0"},
		{"login_count", "integer", "int64", "", false, true, "0"},
		{"score", "real", "*float64", "", true, false, ""},
		{"avatar", "blob", "[]byte", "", true, false, ""},
		{"record_state", "text", "string", "", false, true, "active"},
		{"created_at", "text", "time.Time", "time", false, true, "datetime('now')"},
		{"last_login_at", "text", "*time.Time", "time", true, false, ""},
	}
	for _, tc := range cases {
		col := columnByName(t, users, tc.name)
		if col.DBType != tc.dbType {
			t.Errorf("%s: DBType = %q, want %q", tc.name, col.DBType, tc.dbType)
		}
		if col.GoType != tc.goType {
			t.Errorf("%s: GoType = %q, want %q", tc.name, col.GoType, tc.goType)
		}
		if col.GoImport != tc.goImport {
			t.Errorf("%s: GoImport = %q, want %q", tc.name, col.GoImport, tc.goImport)
		}
		if col.IsNullable != tc.nullable {
			t.Errorf("%s: IsNullable = %v, want %v", tc.name, col.IsNullable, tc.nullable)
		}
		if col.HasDefault != tc.hasDefault {
			t.Errorf("%s: HasDefault = %v, want %v", tc.name, col.HasDefault, tc.hasDefault)
		}
		if col.DefaultValue != tc.defaultVal {
			t.Errorf("%s: DefaultValue = %q, want %q", tc.name, col.DefaultValue, tc.defaultVal)
		}
	}

	if pk := users.PrimaryKey; pk == nil {
		t.Error("users: PrimaryKey is nil")
	} else {
		if pk.Column != "user_id" || len(pk.Columns) != 1 {
			t.Errorf("users: PK = %+v, want single column user_id", pk)
		}
		if pk.GoType != "string" {
			t.Errorf("users: PK GoType = %q, want %q", pk.GoType, "string")
		}
	}

	email := columnByName(t, users, "email")
	if !email.IsUnique {
		t.Error("email: IsUnique = false, want true (UNIQUE column constraint)")
	}
	if email.ValidationTags != "required,email" {
		t.Errorf("email: ValidationTags = %q, want %q", email.ValidationTags, "required,email")
	}

	displayName := columnByName(t, users, "display_name")
	if displayName.MaxLength != 255 {
		t.Errorf("display_name: MaxLength = %d, want 255", displayName.MaxLength)
	}

	userID := columnByName(t, users, "user_id")
	if !userID.IsPrimaryKey {
		t.Error("user_id: IsPrimaryKey = false, want true")
	}
	if userID.IsAutoIncrement {
		t.Error("user_id: IsAutoIncrement = true, want false (TEXT PK is not a rowid alias)")
	}
}

func TestReflectUsersIndexes(t *testing.T) {
	s := reflectFixture(t)
	users := s.Tables["users"]
	if users == nil {
		t.Fatal("users table not reflected")
	}

	var stateIdx *schema.IndexInfo
	var uniqueIdx *schema.IndexInfo
	for i := range users.Indexes {
		idx := &users.Indexes[i]
		switch {
		case idx.Name == "idx_users_record_state":
			stateIdx = idx
		case idx.Unique:
			uniqueIdx = idx
		}
	}

	if stateIdx == nil {
		t.Fatal("idx_users_record_state not reflected")
	}
	if stateIdx.Unique {
		t.Error("idx_users_record_state: Unique = true, want false")
	}
	if stateIdx.Method != "btree" {
		t.Errorf("idx_users_record_state: Method = %q, want %q", stateIdx.Method, "btree")
	}
	if len(stateIdx.Columns) != 1 || stateIdx.Columns[0] != "record_state" {
		t.Errorf("idx_users_record_state: Columns = %v, want [record_state]", stateIdx.Columns)
	}
	if stateIdx.Definition == "" {
		t.Error("idx_users_record_state: Definition is empty, want CREATE INDEX SQL")
	}

	if uniqueIdx == nil {
		t.Fatal("unique index backing the email UNIQUE constraint not reflected")
	}
	if len(uniqueIdx.Columns) != 1 || uniqueIdx.Columns[0] != "email" {
		t.Errorf("unique index: Columns = %v, want [email]", uniqueIdx.Columns)
	}
}

func TestReflectForeignKeys(t *testing.T) {
	s := reflectFixture(t)

	memberships := s.Tables["memberships"]
	if memberships == nil {
		t.Fatal("memberships table not reflected")
	}

	if pk := memberships.PrimaryKey; pk == nil {
		t.Error("memberships: PrimaryKey is nil")
	} else if len(pk.Columns) != 2 || pk.Columns[0] != "org_id" || pk.Columns[1] != "user_id" {
		t.Errorf("memberships: PK Columns = %v, want [org_id user_id]", pk.Columns)
	}

	if len(memberships.ForeignKeys) != 2 {
		t.Fatalf("memberships: %d FKs, want 2", len(memberships.ForeignKeys))
	}
	byRef := make(map[string]schema.ForeignKeyInfo)
	for _, fk := range memberships.ForeignKeys {
		byRef[fk.RefTable] = fk
	}

	userFK, ok := byRef["users"]
	if !ok {
		t.Fatal("memberships: FK to users not reflected")
	}
	if userFK.OnDelete != "CASCADE" {
		t.Errorf("memberships→users: OnDelete = %q, want %q", userFK.OnDelete, "CASCADE")
	}
	if userFK.OnUpdate != "NO_ACTION" {
		t.Errorf("memberships→users: OnUpdate = %q, want %q", userFK.OnUpdate, "NO_ACTION")
	}
	if userFK.ColumnName != "user_id" || userFK.RefColumn != "user_id" {
		t.Errorf("memberships→users: cols = %s→%s, want user_id→user_id", userFK.ColumnName, userFK.RefColumn)
	}
	if userFK.RefSchema != "public" {
		t.Errorf("memberships→users: RefSchema = %q, want %q", userFK.RefSchema, "public")
	}

	// REFERENCES orgs (no column list) must resolve to the parent's PK.
	orgFK, ok := byRef["orgs"]
	if !ok {
		t.Fatal("memberships: FK to orgs not reflected")
	}
	if len(orgFK.RefColumns) != 1 || orgFK.RefColumns[0] != "org_id" {
		t.Errorf("memberships→orgs: RefColumns = %v, want [org_id] (implicit PK target)", orgFK.RefColumns)
	}

	for _, name := range []string{"org_id", "user_id"} {
		if !columnByName(t, memberships, name).IsForeignKey {
			t.Errorf("memberships.%s: IsForeignKey = false, want true", name)
		}
	}

	// Composite FK.
	notes := s.Tables["membership_notes"]
	if notes == nil {
		t.Fatal("membership_notes table not reflected")
	}
	if len(notes.ForeignKeys) != 1 {
		t.Fatalf("membership_notes: %d FKs, want 1", len(notes.ForeignKeys))
	}
	fk := notes.ForeignKeys[0]
	if len(fk.Columns) != 2 || fk.Columns[0] != "org_id" || fk.Columns[1] != "user_id" {
		t.Errorf("composite FK: Columns = %v, want [org_id user_id]", fk.Columns)
	}
	if fk.RefTable != "memberships" {
		t.Errorf("composite FK: RefTable = %q, want %q", fk.RefTable, "memberships")
	}
	if len(fk.RefColumns) != 2 || fk.RefColumns[0] != "org_id" || fk.RefColumns[1] != "user_id" {
		t.Errorf("composite FK: RefColumns = %v, want [org_id user_id]", fk.RefColumns)
	}
	if fk.OnDelete != "CASCADE" {
		t.Errorf("composite FK: OnDelete = %q, want %q", fk.OnDelete, "CASCADE")
	}
	if fk.ConstraintName == "" {
		t.Error("composite FK: ConstraintName is empty, want a synthesized name")
	}

	// INTEGER PRIMARY KEY is a rowid alias.
	noteID := columnByName(t, notes, "note_id")
	if noteID.GoType != "int64" {
		t.Errorf("note_id: GoType = %q, want %q", noteID.GoType, "int64")
	}
	if !noteID.IsAutoIncrement || noteID.AutoIncrementType != "ROWID" {
		t.Errorf("note_id: auto-increment = (%v, %q), want (true, ROWID)",
			noteID.IsAutoIncrement, noteID.AutoIncrementType)
	}
}

func TestResolvePath(t *testing.T) {
	cases := []struct {
		raw, root, want string
	}{
		{"data/app.db", "/proj", "/proj/data/app.db"},
		{"./data/app.db", "/proj", "/proj/data/app.db"},
		{"/var/lib/app.db", "/proj", "/var/lib/app.db"},
		{"sqlite://data/app.db", "/proj", "/proj/data/app.db"},
		{"sqlite:///var/lib/app.db", "/proj", "/var/lib/app.db"},
		{"file:data/app.db?mode=ro", "/proj", "file:data/app.db?mode=ro"},
	}
	for _, tc := range cases {
		if got := ResolvePath(tc.raw, tc.root); got != tc.want {
			t.Errorf("ResolvePath(%q, %q) = %q, want %q", tc.raw, tc.root, got, tc.want)
		}
	}
}

// usersQueries is a minimal queries.sql against the fixture's users table,
// covering the standard verbs the generators resolve.
const usersQueries = `-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(email, display_name)
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM users
WHERE user_id = @user_id
;

-- @func: Update
-- @fields: *,-user_id,-record_state,-created_at
UPDATE users
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: Delete
DELETE FROM users
WHERE user_id = @user_id
;
`

// TestReflectRoundTripResolve verifies the generators accept a sqlite
// snapshot exactly as they accept a Postgres one.
func TestReflectRoundTripResolve(t *testing.T) {
	s := reflectFixture(t)

	qf, err := generators.ParseString(usersQueries)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	qf.Table = "users"

	resolved, err := generators.Resolve(qf, s, "auth")
	if err != nil {
		t.Fatalf("Resolve rejected the sqlite snapshot: %v", err)
	}

	if resolved.TableName != "users" {
		t.Errorf("TableName = %q, want %q", resolved.TableName, "users")
	}
	if resolved.SchemaName != "public" {
		t.Errorf("SchemaName = %q, want %q", resolved.SchemaName, "public")
	}
	if len(resolved.Queries) != 4 {
		t.Errorf("resolved %d queries, want 4", len(resolved.Queries))
	}
	if resolved.PKColumn != "user_id" {
		t.Errorf("resolved PKColumn = %q, want %q", resolved.PKColumn, "user_id")
	}
	if resolved.PKGoType != "string" {
		t.Errorf("resolved PKGoType = %q, want %q", resolved.PKGoType, "string")
	}
}
