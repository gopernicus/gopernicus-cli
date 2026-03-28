package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/fwsource"
	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/goversion"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/tui"
)

func init() {
	RegisterCommand(&Command{
		Name:  "init",
		Short: "Bootstrap a new gopernicus project",
		Long: `Bootstrap a new gopernicus project in a new directory.

Scaffolds a project directory with go.mod, gopernicus.yml, and a minimal
directory layout ready for 'gopernicus generate'.

Examples:
  gopernicus init myapp
  gopernicus init myapp --module github.com/acme/myapp
  gopernicus init myapp --no-interactive
  gopernicus init myapp --no-interactive --features=authentication,authorization
  gopernicus init myapp --no-interactive --features=none
  gopernicus init myapp --framework-version v0.1.0`,
		Usage: "gopernicus init <project-name> [--module <path>] [--framework-version <version>] [--no-interactive] [--features <list>]",
		Run:   runInit,
	})
}

// featureSelection tracks which framework features the user wants bootstrapped.
type featureSelection struct {
	Authentication bool
	Authorization  bool
	Tenancy        bool
	EventsOutbox   bool
}

// allFeatures returns a selection with everything enabled (the default).
func allFeatures() featureSelection {
	return featureSelection{
		Authentication: true,
		Authorization:  true,
		Tenancy:        true,
		EventsOutbox:   true, // on by default — transactional outbox for reliable events
	}
}

// noFeatures returns a selection with everything disabled.
func noFeatures() featureSelection {
	return featureSelection{}
}

// any returns true if at least one feature is selected.
func (f featureSelection) any() bool {
	return f.Authentication || f.Authorization || f.Tenancy || f.EventsOutbox
}

// infrastructureSelection tracks which infrastructure adapters to bootstrap.
type infrastructureSelection struct {
	HasRedis       bool // Redis client (enables redis cache; required for Redis Streams)
	HasRedisStreams bool // Redis Streams event bus backend
	HasStorageDisk bool // Local disk file storage
	HasStorageGCS  bool // Google Cloud Storage
	HasStorageS3   bool // AWS S3 / compatible
	HasSendGrid    bool // SendGrid email delivery
}

// defaultInfrastructure returns the default infrastructure selection.
// Redis + disk + GCS + SendGrid are on by default (matching the docker-compose setup).
func defaultInfrastructure() infrastructureSelection {
	return infrastructureSelection{
		HasRedis:        true,
		HasRedisStreams:  true,
		HasStorageDisk:  true,
		HasStorageGCS:   true,
		HasSendGrid:     true,
	}
}

func runInit(_ context.Context, args []string) error {
	opts, err := parseInitArgs(args)
	if err != nil {
		return err
	}

	if err := resolveInitOpts(&opts); err != nil {
		return err
	}

	target, err := scaffoldProject(opts)
	if err != nil {
		return err
	}

	// Copy feature assets (migrations, repos, bridges) from gopernicus source.
	if opts.features.any() {
		if err := copyFeatureAssets(target, opts.modulePath, opts.frameworkVersion, opts.features); err != nil {
			return err
		}
	}

	// Add gopernicus as a dependency.
	if devSource := os.Getenv("GOPERNICUS_DEV_SOURCE"); devSource != "" {
		// Dev mode: replace directive pointing to local gopernicus source.
		fmt.Printf("  → linking local gopernicus (%s)\n", devSource)
		replace := exec.Command("go", "mod", "edit",
			"-replace=github.com/gopernicus/gopernicus="+devSource,
		)
		replace.Dir = target
		replace.Stdout = os.Stdout
		replace.Stderr = os.Stderr
		if err := replace.Run(); err != nil {
			return fmt.Errorf("go mod edit -replace: %w", err)
		}
	} else {
		fwRef := "github.com/gopernicus/gopernicus@latest"
		if opts.frameworkVersion != "" {
			fwRef = "github.com/gopernicus/gopernicus@" + opts.frameworkVersion
		}
		fmt.Printf("  → adding gopernicus framework\n")
		goGet := exec.Command("go", "get", fwRef)
		goGet.Dir = target
		goGet.Stdout = os.Stdout
		goGet.Stderr = os.Stderr
		if err := goGet.Run(); err != nil {
			fmt.Printf("  warning: go get failed: %v\n", err)
		}
	}

	// Run go mod tidy to clean up dependencies.
	fmt.Printf("  → running go mod tidy\n")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = target
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		fmt.Printf("  warning: go mod tidy failed: %v\n", err)
	}

	fmt.Println()
	fmt.Printf("  ✓ created %s\n\n", opts.projectName)
	fmt.Printf("  cd %s\n", opts.projectName)
	fmt.Printf("  gopernicus doctor   # check project health\n")
	fmt.Println()

	return nil
}

// initOpts holds all inputs for the init command.
type initOpts struct {
	projectName      string
	orgHint          string // extracted from "org/name" format (e.g. "jrazmi" from "jrazmi/foo")
	modulePath       string
	frameworkVersion string // gopernicus framework version (e.g. "v0.1.0"); "" means latest
	noInteractive    bool
	featuresFlag     string // raw --features value; "" means use default (all)
	features         featureSelection
	infra            infrastructureSelection
}

func parseInitArgs(args []string) (initOpts, error) {
	var opts initOpts
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--module" || args[i] == "-m":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--module requires a value")
			}
			i++
			opts.modulePath = args[i]
		case args[i] == "--no-interactive":
			opts.noInteractive = true
		case strings.HasPrefix(args[i], "--features="):
			opts.featuresFlag = strings.TrimPrefix(args[i], "--features=")
		case args[i] == "--features":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--features requires a value")
			}
			i++
			opts.featuresFlag = args[i]
		case strings.HasPrefix(args[i], "--framework-version="):
			opts.frameworkVersion = strings.TrimPrefix(args[i], "--framework-version=")
		case args[i] == "--framework-version":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--framework-version requires a value")
			}
			i++
			opts.frameworkVersion = args[i]
		case strings.HasPrefix(args[i], "--"):
			return opts, fmt.Errorf("unknown flag %q", args[i])
		default:
			if opts.projectName == "" {
				raw := args[i]
				// "org/repo" or "github.com/org/repo" → extract repo name and infer module path.
				if idx := strings.LastIndex(raw, "/"); idx >= 0 {
					opts.orgHint = raw[:idx]
					opts.projectName = raw[idx+1:]
				} else {
					opts.projectName = raw
				}
			}
		}
	}
	return opts, nil
}

func resolveInitOpts(opts *initOpts) error {
	interactive := !opts.noInteractive && tui.IsInteractive()

	if interactive {
		return resolveInitOptsInteractive(opts)
	}
	return resolveInitOptsPlain(opts)
}

func resolveInitOptsInteractive(opts *initOpts) error {
	defaultModule := ""
	switch {
	case opts.modulePath != "":
		defaultModule = opts.modulePath
	case opts.orgHint != "" && opts.projectName != "":
		// Infer github.com/<org>/<repo> from "org/repo" shorthand.
		if strings.Contains(opts.orgHint, ".") {
			defaultModule = opts.orgHint + "/" + opts.projectName
		} else {
			defaultModule = "github.com/" + opts.orgHint + "/" + opts.projectName
		}
	case opts.projectName != "":
		defaultModule = "github.com/your-org/" + opts.projectName
	}

	fields := []tui.WizardField{
		{
			Label:       "Project name",
			Placeholder: "myapp",
			Default:     opts.projectName,
			Validate:    validateProjectName,
		},
		{
			Label:       "Go module path",
			Placeholder: "github.com/your-org/myapp",
			Default:     defaultModule,
			Validate:    validateModulePath,
		},
	}

	result, err := tui.RunWizard("gopernicus init", fields)
	if err != nil {
		return err
	}
	if result.Cancelled {
		return fmt.Errorf("cancelled")
	}

	opts.projectName = result.Values[0]
	opts.modulePath = result.Values[1]

	// Feature picker — skip if --features was provided on CLI.
	if opts.featuresFlag != "" {
		features, err := parseFeaturesFlag(opts.featuresFlag)
		if err != nil {
			return err
		}
		opts.features = features
	} else {
		features, err := runFeaturePicker()
		if err != nil {
			return err
		}
		opts.features = features
	}

	// Infrastructure picker — always shown interactively.
	infra, err := runInfraPicker()
	if err != nil {
		return err
	}
	opts.infra = infra

	return nil
}

func resolveInitOptsPlain(opts *initOpts) error {
	if opts.projectName == "" {
		return fmt.Errorf("project name required\n\nUsage: gopernicus init <project-name>")
	}
	if err := validateProjectName(opts.projectName); err != nil {
		return err
	}
	if opts.modulePath == "" {
		if opts.orgHint != "" {
			if strings.Contains(opts.orgHint, ".") {
				opts.modulePath = opts.orgHint + "/" + opts.projectName
			} else {
				opts.modulePath = "github.com/" + opts.orgHint + "/" + opts.projectName
			}
		} else {
			opts.modulePath = "github.com/your-org/" + opts.projectName
			fmt.Printf("note: using module path %q — edit go.mod to change it\n", opts.modulePath)
		}
	}

	// Default: all features enabled unless --features flag was provided.
	if opts.featuresFlag != "" {
		features, err := parseFeaturesFlag(opts.featuresFlag)
		if err != nil {
			return err
		}
		opts.features = features
	} else {
		opts.features = allFeatures()
	}

	// Use defaults for infrastructure in non-interactive mode.
	opts.infra = defaultInfrastructure()

	return nil
}

// parseFeaturesFlag parses the --features flag value.
// Accepts: "none", "authentication", "authorization", "tenancy", "events-outbox", or comma-separated.
func parseFeaturesFlag(value string) (featureSelection, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "none" {
		return noFeatures(), nil
	}
	if value == "all" || value == "" {
		return allFeatures(), nil
	}

	features := noFeatures()
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "authentication":
			features.Authentication = true
		case "authorization":
			features.Authorization = true
		case "tenancy":
			features.Tenancy = true
		case "events-outbox":
			features.EventsOutbox = true
		default:
			return features, fmt.Errorf("unknown feature %q (valid: authentication, authorization, tenancy, events-outbox, none, all)", name)
		}
	}
	return features, nil
}

// runFeaturePicker shows per-screen interactive multi-selects for framework features.
// Each category gets its own TUI screen.
func runFeaturePicker() (featureSelection, error) {
	// Screen 1: Framework Features
	r1, err := tui.RunPicker("Framework Features", []tui.PickerCategory{
		{
			Name: "Framework Features",
			Items: []tui.PickerItem{
				{Name: "Authentication", Description: "users, sessions, OAuth, API keys", Selected: true},
				{Name: "Authorization", Description: "ReBAC relationships, permissions", Selected: true},
				{Name: "Tenancy", Description: "multi-tenant isolation, groups", Selected: true},
			},
		},
	})
	if err != nil {
		return noFeatures(), err
	}
	if r1.Cancelled {
		return noFeatures(), fmt.Errorf("cancelled")
	}

	// Screen 2: Event Infrastructure
	r2, err := tui.RunPicker("Event Infrastructure", []tui.PickerCategory{
		{
			Name: "Event Infrastructure",
			Items: []tui.PickerItem{
				{Name: "Events Outbox", Description: "durable event delivery via event_outbox table", Selected: true},
			},
		},
	})
	if err != nil {
		return noFeatures(), err
	}
	if r2.Cancelled {
		return noFeatures(), fmt.Errorf("cancelled")
	}

	features := noFeatures()
	for _, name := range r1.Selected {
		switch name {
		case "Authentication":
			features.Authentication = true
		case "Authorization":
			features.Authorization = true
		case "Tenancy":
			features.Tenancy = true
		}
	}
	for _, name := range r2.Selected {
		if name == "Events Outbox" {
			features.EventsOutbox = true
		}
	}
	return features, nil
}

// runInfraPicker shows per-screen interactive multi-selects for infrastructure adapters.
// Each category gets its own TUI screen.
func runInfraPicker() (infrastructureSelection, error) {
	// Screen 1: Cache
	r1, err := tui.RunPicker("Cache Backend", []tui.PickerCategory{
		{
			Name: "Cache Backend",
			Items: []tui.PickerItem{
				{Name: "Redis Cache", Description: "Redis-backed caching (recommended)", Selected: true},
			},
		},
	})
	if err != nil {
		return defaultInfrastructure(), err
	}
	if r1.Cancelled {
		return defaultInfrastructure(), fmt.Errorf("cancelled")
	}

	// Screen 2: Event Bus
	r2, err := tui.RunPicker("Event Bus Backend", []tui.PickerCategory{
		{
			Name: "Event Bus Backend",
			Items: []tui.PickerItem{
				{Name: "Redis Streams", Description: "Durable event bus via Redis Streams", Selected: true},
			},
		},
	})
	if err != nil {
		return defaultInfrastructure(), err
	}
	if r2.Cancelled {
		return defaultInfrastructure(), fmt.Errorf("cancelled")
	}

	// Screen 3: File Storage
	r3, err := tui.RunPicker("File Storage", []tui.PickerCategory{
		{
			Name: "File Storage",
			Items: []tui.PickerItem{
				{Name: "Disk Storage", Description: "Local filesystem storage", Selected: true},
				{Name: "GCS", Description: "Google Cloud Storage", Selected: true},
				{Name: "S3", Description: "AWS S3 / compatible object storage", Selected: false},
			},
		},
	})
	if err != nil {
		return defaultInfrastructure(), err
	}
	if r3.Cancelled {
		return defaultInfrastructure(), fmt.Errorf("cancelled")
	}

	// Screen 4: Email
	r4, err := tui.RunPicker("Email Delivery", []tui.PickerCategory{
		{
			Name: "Email Delivery",
			Items: []tui.PickerItem{
				{Name: "SendGrid", Description: "Production email delivery via SendGrid", Selected: true},
			},
		},
	})
	if err != nil {
		return defaultInfrastructure(), err
	}
	if r4.Cancelled {
		return defaultInfrastructure(), fmt.Errorf("cancelled")
	}

	infra := infrastructureSelection{}
	for _, name := range r1.Selected {
		if name == "Redis Cache" {
			infra.HasRedis = true
		}
	}
	for _, name := range r2.Selected {
		if name == "Redis Streams" {
			infra.HasRedisStreams = true
			infra.HasRedis = true // Redis Streams requires a Redis connection
		}
	}
	for _, name := range r3.Selected {
		switch name {
		case "Disk Storage":
			infra.HasStorageDisk = true
		case "GCS":
			infra.HasStorageGCS = true
		case "S3":
			infra.HasStorageS3 = true
		}
	}
	for _, name := range r4.Selected {
		if name == "SendGrid" {
			infra.HasSendGrid = true
		}
	}
	return infra, nil
}

func scaffoldProject(opts initOpts) (string, error) {
	target, err := filepath.Abs(opts.projectName)
	if err != nil {
		return "", err
	}

	// Refuse to overwrite an existing directory that has content.
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("directory %q already exists and is not empty", opts.projectName)
	}

	// Build the manifest with features and domain mappings.
	m := manifest.NewWithProject(opts.projectName)
	if opts.frameworkVersion != "" {
		m.GopernicusVersion = opts.frameworkVersion
	}
	applyFeatureSelection(m, opts.features)

	steps := []struct {
		desc string
		fn   func() error
	}{
		{"creating project directory", func() error {
			return os.MkdirAll(target, 0755)
		}},
		{"initializing go module", func() error {
			cmd := exec.Command("go", "mod", "init", opts.modulePath)
			cmd.Dir = target
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
			// Pin to the minimum supported Go version so all projects are consistent.
			pin := exec.Command("go", "mod", "edit", "-go="+goversion.MinGoVersion)
			pin.Dir = target
			return pin.Run()
		}},
		{"creating directory layout", func() error {
			dirs := []string{
				manifest.MigrationsDir("primary"),
				"core/repositories",
				"core/cases",
				"core/transit",
				"core/auth",
				"bridge/repositories",
				"bridge/cases",
				"bridge/transit",
				"infrastructure",
				"sdk",
				"workshop/dev",
				"workshop/testing/fixtures",
				"workshop/testing/e2e",
			}
			for _, d := range dirs {
				if err := os.MkdirAll(filepath.Join(target, d), 0755); err != nil {
					return err
				}
			}
			return nil
		}},
		{"writing gopernicus.yml", func() error {
			return manifest.Save(target, m)
		}},
		{"writing .gitignore", func() error {
			return os.WriteFile(
				filepath.Join(target, ".gitignore"),
				[]byte(defaultGitignore),
				0644,
			)
		}},
		{"scaffolding app server", func() error {
			hasStorage := opts.infra.HasStorageDisk || opts.infra.HasStorageGCS || opts.infra.HasStorageS3
			return generators.GenerateAppScaffold(target, generators.AppScaffoldData{
				ProjectName:       opts.projectName,
				ModulePath:        opts.modulePath,
				AppNameUpper:      generators.AppNameFromProject(opts.projectName),
				HasAuthentication: opts.features.Authentication,
				HasAuthorization:  opts.features.Authorization,
				HasTenancy:        opts.features.Tenancy,
				HasOutbox:         opts.features.EventsOutbox,
				HasRedis:          opts.infra.HasRedis,
				HasRedisStreams:    opts.infra.HasRedisStreams,
				HasStorageDisk:    opts.infra.HasStorageDisk,
				HasStorageGCS:     opts.infra.HasStorageGCS,
				HasStorageS3:      opts.infra.HasStorageS3,
				HasSendGrid:       opts.infra.HasSendGrid,
				HasStorage:        hasStorage,
			})
		}},
	}

	fmt.Println()
	for _, step := range steps {
		fmt.Printf("  → %s\n", step.desc)
		if err := step.fn(); err != nil {
			return "", fmt.Errorf("%s: %w", step.desc, err)
		}
	}

	return target, nil
}

// applyFeatureSelection configures the manifest based on selected features.
func applyFeatureSelection(m *manifest.Manifest, features featureSelection) {
	if m.Features == nil {
		m.Features = &manifest.FeaturesConfig{}
	}

	if features.Authentication {
		m.Features.Authentication = manifest.FeatureGopernicus
	} else {
		m.Features.Authentication = ""
	}

	if features.Authorization {
		m.Features.Authorization = manifest.FeatureGopernicus
	} else {
		m.Features.Authorization = ""
	}

	if features.Tenancy {
		m.Features.Tenancy = manifest.FeatureGopernicus
	} else {
		m.Features.Tenancy = ""
	}

	if features.EventsOutbox {
		if m.Events == nil {
			m.Events = &manifest.EventsConfig{}
		}
		m.Events.Outbox = manifest.FeatureGopernicus
	}

	// Set domain mappings for selected features.
	db := m.DatabaseOrDefault("")
	if db == nil {
		return
	}
	if db.Domains == nil {
		db.Domains = make(map[string][]string)
	}

	if features.Authentication {
		db.Domains["auth"] = []string{
			"api_keys",
			"oauth_accounts",
			"principals",
			"security_events",
			"service_accounts",
			"sessions",
			"user_passwords",
			"users",
			"verification_codes",
			"verification_tokens",
		}
	}

	if features.Authorization {
		db.Domains["rebac"] = []string{
			"groups",
			"invitations",
			"rebac_relationships",
			"rebac_relationship_metadata",
		}
	}

	if features.Tenancy {
		db.Domains["tenancy"] = []string{
			"tenants",
		}
	}

	if features.EventsOutbox {
		db.Domains["events"] = []string{
			"event_outbox",
		}
	}
}

// copyFeatureAssets copies migrations, core repositories, and bridge
// repositories from the gopernicus framework source into the new project.
// Go files have their import paths rewritten from the gopernicus module to
// the user's module path.
func copyFeatureAssets(target, modulePath, fwVersion string, features featureSelection) error {
	source, err := gopernicusSourceDir(fwVersion)
	if err != nil {
		return fmt.Errorf("resolving gopernicus source: %w", err)
	}

	const gopernicusModule = "github.com/gopernicus/gopernicus"

	// Copy migrations.
	type migration struct {
		name string
		file string
	}
	migrations := []migration{
		{"authentication", "0001_auth.sql"},
		{"authorization", "0002_rebac.sql"},
		{"tenancy", "0003_tenants.sql"},
		{"events-outbox", "0004_events.sql"},
	}

	for _, mig := range migrations {
		enabled := false
		switch mig.name {
		case "authentication":
			enabled = features.Authentication
		case "authorization":
			enabled = features.Authorization
		case "tenancy":
			enabled = features.Tenancy
		case "events-outbox":
			enabled = features.EventsOutbox
		}
		if !enabled {
			continue
		}

		src := filepath.Join(source, "workshop", "migrations", "primary", mig.file)
		dst := filepath.Join(target, manifest.MigrationsDir("primary"), mig.file)

		fmt.Printf("  → copying %s migration\n", mig.name)
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copying %s migration: %w", mig.name, err)
		}
	}

	// Copy core repositories and bridge repositories.
	type domainSource struct {
		featureName string
		domain      string // directory name under core/repositories/
		bridgeDir   string // directory name under bridge/repositories/
	}
	domains := []domainSource{
		{"authentication", "auth", "authreposbridge"},
		{"authorization", "rebac", "rebacreposbridge"},
		{"tenancy", "tenancy", "tenancyreposbridge"},
		{"events-outbox", "events", "eventsreposbridge"},
	}

	for _, d := range domains {
		enabled := false
		switch d.featureName {
		case "authentication":
			enabled = features.Authentication
		case "authorization":
			enabled = features.Authorization
		case "tenancy":
			enabled = features.Tenancy
		case "events-outbox":
			enabled = features.EventsOutbox
		}
		if !enabled {
			continue
		}

		// Copy core/repositories/{domain}/
		fmt.Printf("  → copying %s core repositories\n", d.featureName)
		coreSrc := filepath.Join(source, "core", "repositories", d.domain)
		coreDst := filepath.Join(target, "core", "repositories", d.domain)
		if err := copyDirRecursive(coreSrc, coreDst); err != nil {
			return fmt.Errorf("copying %s core repositories: %w", d.featureName, err)
		}

		// Copy bridge/repositories/{domain}reposbridge/
		bridgeSrc := filepath.Join(source, "bridge", "repositories", d.bridgeDir)
		if _, err := os.Stat(bridgeSrc); err == nil {
			fmt.Printf("  → copying %s bridge repositories\n", d.featureName)
			bridgeDst := filepath.Join(target, "bridge", "repositories", d.bridgeDir)
			if err := copyDirRecursive(bridgeSrc, bridgeDst); err != nil {
				return fmt.Errorf("copying %s bridge repositories: %w", d.featureName, err)
			}
		}
	}

	// Copy satisfiers into their respective domain packages.
	if features.Authentication {
		fmt.Printf("  → copying authentication satisfiers\n")
		satSrc := filepath.Join(source, "core", "auth", "authentication", "satisfiers")
		satDst := filepath.Join(target, "core", "auth", "authentication", "satisfiers")
		if err := copyDirRecursive(satSrc, satDst); err != nil {
			return fmt.Errorf("copying authentication satisfiers: %w", err)
		}

		fmt.Printf("  → copying authentication bridge\n")
		authBridgeSrc := filepath.Join(source, "bridge", "auth", "authentication")
		authBridgeDst := filepath.Join(target, "bridge", "auth", "authentication")
		if err := copyDirRecursive(authBridgeSrc, authBridgeDst); err != nil {
			return fmt.Errorf("copying authentication bridge: %w", err)
		}
	}
	if features.Authorization {
		fmt.Printf("  → copying authorization satisfiers\n")
		satSrc := filepath.Join(source, "core", "auth", "authorization", "satisfiers")
		satDst := filepath.Join(target, "core", "auth", "authorization", "satisfiers")
		if err := copyDirRecursive(satSrc, satDst); err != nil {
			return fmt.Errorf("copying authorization satisfiers: %w", err)
		}

		fmt.Printf("  → copying invitations bridge\n")
		invBridgeSrc := filepath.Join(source, "bridge", "auth", "invitations")
		invBridgeDst := filepath.Join(target, "bridge", "auth", "invitations")
		if err := copyDirRecursive(invBridgeSrc, invBridgeDst); err != nil {
			return fmt.Errorf("copying invitations bridge: %w", err)
		}
	}
	if features.EventsOutbox {
		fmt.Printf("  → copying events satisfiers\n")
		satSrc := filepath.Join(source, "core", "transit", "events", "satisfiers")
		satDst := filepath.Join(target, "core", "transit", "events", "satisfiers")
		if err := copyDirRecursive(satSrc, satDst); err != nil {
			return fmt.Errorf("copying events satisfiers: %w", err)
		}
	}

	// Rewrite import paths in all copied .go files.
	if modulePath != gopernicusModule {
		fmt.Printf("  → rewriting import paths\n")
		for _, layer := range []string{"core/repositories", "core/auth/authentication/satisfiers", "core/auth/authorization/satisfiers", "core/transit/events/satisfiers", "bridge/repositories", "bridge/auth"} {
			dir := filepath.Join(target, layer)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			if err := rewriteImports(dir, gopernicusModule, modulePath); err != nil {
				return fmt.Errorf("rewriting imports in %s: %w", layer, err)
			}
		}
	}

	return nil
}

// gopernicusSourceDir returns the path to the gopernicus framework source.
// Uses GOPERNICUS_DEV_SOURCE if set, otherwise resolves from the Go module
// cache via `go mod download -json`. When version is non-empty it fetches
// that specific version; otherwise it fetches @latest.
func gopernicusSourceDir(version string) (string, error) {
	return fwsource.ResolveDirVersion(version)
}

// copyDirRecursive copies all files and subdirectories from src to dst.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		return copyFile(path, target)
	})
}

// rewriteImports replaces oldModule with newModule in all .go files under dir.
// Only rewrites imports for core/repositories and bridge/repositories paths —
// framework SDK/infrastructure imports are left pointing at gopernicus.
func rewriteImports(dir, oldModule, newModule string) error {
	oldCore := oldModule + "/core/repositories/"
	newCore := newModule + "/core/repositories/"
	oldAuthSatisfiers := oldModule + "/core/auth/authentication/satisfiers"
	newAuthSatisfiers := newModule + "/core/auth/authentication/satisfiers"
	oldAuthzSatisfiers := oldModule + "/core/auth/authorization/satisfiers"
	newAuthzSatisfiers := newModule + "/core/auth/authorization/satisfiers"
	oldBridge := oldModule + "/bridge/repositories/"
	newBridge := newModule + "/bridge/repositories/"

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		content := string(data)
		updated := strings.ReplaceAll(content, oldCore, newCore)
		updated = strings.ReplaceAll(updated, oldAuthSatisfiers, newAuthSatisfiers)
		updated = strings.ReplaceAll(updated, oldAuthzSatisfiers, newAuthzSatisfiers)
		updated = strings.ReplaceAll(updated, oldBridge, newBridge)

		if updated != content {
			return os.WriteFile(path, []byte(updated), info.Mode())
		}
		return nil
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func validateProjectName(s string) error {
	if s == "" {
		return fmt.Errorf("required")
	}
	for _, c := range s {
		if !isAlphaNumDash(c) {
			return fmt.Errorf("only letters, numbers, and hyphens allowed")
		}
	}
	return nil
}

func validateModulePath(s string) error {
	if s == "" {
		return fmt.Errorf("required")
	}
	if strings.Contains(s, " ") {
		return fmt.Errorf("module path cannot contain spaces")
	}
	return nil
}

func isAlphaNumDash(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_'
}

const defaultGitignore = `# Binaries
*.exe
*.dll
*.so
*.dylib

# Go
*.test
*.out
/vendor/

# Environment
.env
.env.*
!.env.example

# Editor
.DS_Store
.idea/
.vscode/
`
