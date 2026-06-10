package generators

import (
	"strings"
	"testing"
	"text/template"
)

// TestTemplatesParse ensures every generated-code template constant is valid
// text/template syntax. The compiler cannot catch errors inside template
// strings, so without this a malformed {{...}} action only surfaces when a
// user runs generate. Render correctness is still exercised at generation
// time; this test pins syntax validity.
//
// FuncMaps must contain the same names the render functions register —
// text/template resolves function names at parse time. Templates rendered
// with inline FuncMaps (fixtures, integration tests) use stand-ins here.
func TestTemplatesParse(t *testing.T) {
	dummy := func() string { return "" }

	fixtureFuncs := template.FuncMap{
		"lower":             strings.ToLower,
		"camel":             dummy,
		"singularize":       dummy,
		"join":              dummy,
		"positionalArgs":    dummy,
		"questionArgs":      dummy,
		"add":               dummy,
		"insertCols":        dummy,
		"selectCols":        dummy,
		"nonSelfRefParents": dummy,
		"inBatchParents":    dummy,
		"outOfBatchParents": dummy,
	}
	integrationFuncs := template.FuncMap{
		"lower": strings.ToLower,
		"camel": dummy,
	}

	tests := []struct {
		name  string
		src   string
		funcs template.FuncMap
	}{
		{"repoGenerated", repoGeneratedTemplate, repoFuncs},
		{"repoRepository", repoRepositoryTemplate, repoFuncs},
		{"repoFop", repoFopTemplate, repoFuncs},
		{"storeGenerated", storeGeneratedTemplate, nil},
		{"storeBootstrap", storeBootstrapTemplate, nil},
		{"specStoreGenerated", specStoreGeneratedTemplate, nil},
		{"specStoreBootstrap", specStoreBootstrapTemplate, nil},
		{"cacheGenerated", cacheGeneratedTemplate, nil},
		{"cacheBootstrap", cacheBootstrapTemplate, nil},
		{"bridgeGenerated", bridgeGeneratedTemplate, bridgeFuncs},
		{"bridgeRoutes", bridgeRoutesTemplate, bridgeFuncs},
		{"bridgeBridge", bridgeBridgeTemplate, bridgeFuncs},
		{"bridgeHttp", bridgeHttpTemplate, bridgeFuncs},
		{"bridgeFop", bridgeFopTemplate, bridgeFuncs},
		{"compositeGenerated", compositeGeneratedTemplate, nil},
		{"compositeSpecGenerated", compositeSpecGeneratedTemplate, nil},
		{"bridgeCompositeGenerated", bridgeCompositeGeneratedTemplate, nil},
		{"integrationTestGenerated", integrationTestGeneratedTemplate, integrationFuncs},
		{"integrationTestBootstrap", integrationTestBootstrapTemplate, integrationFuncs},
		{"fixtureGenerated", fixtureGeneratedTemplate, fixtureFuncs},
		{"fixtureBootstrap", fixtureBootstrapTemplate, fixtureFuncs},
		{"authSchemaGenerated", authSchemaGeneratedTemplate, nil},
		{"authSchemaBootstrap", authSchemaBootstrapTemplate, nil},
		{"appMain", mainTemplate, nil},
		{"appServer", serverTemplate, nil},
		{"appEmails", emailsTemplate, nil},
		{"appEnvExample", envExampleTemplate, nil},
		{"appDockerCompose", dockerComposeTemplate, nil},
		{"appDockerfile", dockerfileTemplate, nil},
		{"appMakefile", makefileTemplate, nil},
		{"appDocsREADME", documentationREADMETemplate, nil},
		{"appDocsArchOverview", documentationArchOverviewTemplate, nil},
		{"appDocsDeployDocker", documentationDeployDockerTemplate, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := template.New("")
			if tc.funcs != nil {
				tm = tm.Funcs(tc.funcs)
			}
			if _, err := tm.Parse(tc.src); err != nil {
				t.Fatalf("template does not parse: %v", err)
			}
		})
	}
}
