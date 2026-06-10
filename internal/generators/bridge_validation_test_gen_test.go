package generators

import (
	"strings"
	"testing"
)

// caseNames flattens a case slice to its names for substring assertions.
func caseNames(cases []ValidationTestCase) string {
	var b strings.Builder
	for _, c := range cases {
		b.WriteString(c.Name)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestValidationCasesCoverEveryConstraintClass(t *testing.T) {
	data := BridgeTemplateData{
		EntityName: "Account",
		CreateQueries: []BridgeCreateQuery{{
			FuncName: "Create",
			Fields: []BridgeField{
				{DBName: "name", GoName: "Name", GoType: "string", IsString: true, IsRequired: true},
				{DBName: "email", GoName: "Email", GoType: "string", IsString: true, IsEmail: true},
				{DBName: "website", GoName: "Website", GoType: "string", IsString: true, IsURL: true},
				{DBName: "handle", GoName: "Handle", GoType: "string", IsString: true, IsSlug: true},
				{DBName: "tier", GoName: "Tier", GoType: "string", IsString: true, IsEnum: true,
					EnumValues: []string{"free", "pro"}},
				{DBName: "code", GoName: "Code", GoType: "string", IsString: true, MaxLength: 8},
			},
		}},
		UpdateQueries: []BridgeUpdateQuery{{
			FuncName: "Update",
			Fields: []BridgeField{
				{DBName: "email", GoName: "Email", GoType: "string", IsString: true, IsEmail: true},
				{DBName: "code", GoName: "Code", GoType: "string", IsString: true, MaxLength: 8},
			},
		}},
	}

	out := buildBridgeValidationTestData(data)
	got := caseNames(out.CreateCases)

	for _, want := range []string{
		"missing required name",
		"email rejects malformed email",
		"website rejects malformed url",
		"handle rejects malformed slug",
		"tier rejects value outside enum",
		"code rejects value over max length",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("create cases missing %q\n--- got ---\n%s", want, got)
		}
	}

	// The valid baseline must satisfy every constraint at once.
	validAssigns := map[string]string{}
	for _, a := range out.CreateValidAssigns {
		validAssigns[a.GoName] = a.ValueExpr
	}
	if validAssigns["Email"] != `"user@example.com"` {
		t.Errorf("valid email assign = %q, want a valid email literal", validAssigns["Email"])
	}
	if validAssigns["Tier"] != `"free"` {
		t.Errorf("valid enum assign = %q, want the first enum value", validAssigns["Tier"])
	}

	// Update cases are pointer-wrapped (Ptr validators skip nil), and
	// Required is not enforced on update.
	upd := caseNames(out.UpdateCases)
	if !strings.Contains(upd, "update email rejects malformed email") {
		t.Errorf("update cases missing the email probe\n--- got ---\n%s", upd)
	}
	if strings.Contains(upd, "missing required") {
		t.Error("update must not assert required-field presence")
	}
	for _, c := range out.UpdateCases {
		if !strings.Contains(c.ValueExpr, "testPtr(") {
			t.Errorf("update case %q must wrap its value in testPtr (update fields are pointers)", c.Name)
		}
	}
}
