package cmd

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"github.com/gopernicus/gopernicus/workshop/codegen/initialize"
	"github.com/gopernicus/gopernicus-cli/internal/tui"
)

func init() {
	cli.RegisterCommand(&cli.Command{
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

// runInit resolves init options — interactively via the TUI pickers when a
// terminal is attached, plainly otherwise — and hands them to the framework's
// init engine, which does the scaffolding and orchestration.
func runInit(_ context.Context, args []string) error {
	opts, err := initialize.ParseArgs(args)
	if err != nil {
		return err
	}

	if err := resolveInitOpts(&opts); err != nil {
		return err
	}

	return initialize.Run(opts)
}

func resolveInitOpts(opts *initialize.Options) error {
	interactive := !opts.NoInteractive && tui.IsInteractive()

	if interactive {
		return resolveInitOptsInteractive(opts)
	}
	return initialize.ResolveDefaults(opts)
}

func resolveInitOptsInteractive(opts *initialize.Options) error {
	defaultModule := opts.ModulePath
	if defaultModule == "" {
		defaultModule = initialize.DefaultModulePath(opts.OrgHint, opts.ProjectName)
	}

	fields := []tui.WizardField{
		{
			Label:       "Project name",
			Placeholder: "myapp",
			Default:     opts.ProjectName,
			Validate:    initialize.ValidateProjectName,
		},
		{
			Label:       "Go module path",
			Placeholder: "github.com/your-org/myapp",
			Default:     defaultModule,
			Validate:    initialize.ValidateModulePath,
		},
	}

	result, err := tui.RunWizard("gopernicus init", fields)
	if err != nil {
		return err
	}
	if result.Cancelled {
		return fmt.Errorf("cancelled")
	}

	opts.ProjectName = result.Values[0]
	opts.ModulePath = result.Values[1]

	// Feature picker — skip if --features was provided on CLI.
	if opts.FeaturesFlag != "" {
		features, err := initialize.ParseFeaturesFlag(opts.FeaturesFlag)
		if err != nil {
			return err
		}
		opts.Features = features
	} else {
		features, err := runFeaturePicker()
		if err != nil {
			return err
		}
		opts.Features = features
	}

	// Infrastructure picker — always shown interactively.
	infra, err := runInfraPicker()
	if err != nil {
		return err
	}
	opts.Infra = infra

	// AI companion picker — always shown interactively.
	ai, err := runAICompanionPicker()
	if err != nil {
		return err
	}
	opts.AI = ai

	return nil
}

// pickerScreen is one interactive multi-select screen: a title (also the
// single category name), its items, and the selection flags each item name
// sets when chosen.
type pickerScreen struct {
	Title string
	Items []tui.PickerItem
	Apply map[string][]*bool
}

// runPickerScreens shows each screen in order and applies selections to the
// target flags. Returns an error when a screen fails or is cancelled.
func runPickerScreens(screens []pickerScreen) error {
	for _, s := range screens {
		r, err := tui.RunPicker(s.Title, []tui.PickerCategory{
			{Name: s.Title, Items: s.Items},
		})
		if err != nil {
			return err
		}
		if r.Cancelled {
			return fmt.Errorf("cancelled")
		}
		for _, name := range r.Selected {
			for _, flag := range s.Apply[name] {
				*flag = true
			}
		}
	}
	return nil
}

// runFeaturePicker shows per-screen interactive multi-selects for framework features.
// Each category gets its own TUI screen.
func runFeaturePicker() (initialize.FeatureSelection, error) {
	features := initialize.NoFeatures()
	screens := []pickerScreen{
		{
			Title: "Framework Features",
			Items: []tui.PickerItem{
				{Name: "Authentication", Description: "users, sessions, OAuth, API keys", Selected: true},
				{Name: "Authorization", Description: "ReBAC relationships, permissions", Selected: true},
				{Name: "Tenancy", Description: "multi-tenant isolation, groups", Selected: true},
			},
			Apply: map[string][]*bool{
				"Authentication": {&features.Authentication},
				"Authorization":  {&features.Authorization},
				"Tenancy":        {&features.Tenancy},
			},
		},
		{
			Title: "Event Infrastructure",
			Items: []tui.PickerItem{
				{Name: "Event Outbox", Description: "transactional outbox for atomic event delivery", Selected: true},
				{Name: "Job Queue", Description: "durable deferred processing with retry and dead-lettering", Selected: true},
			},
			Apply: map[string][]*bool{
				"Event Outbox": {&features.EventOutbox},
				"Job Queue":    {&features.JobQueue},
			},
		},
	}
	if err := runPickerScreens(screens); err != nil {
		return initialize.NoFeatures(), err
	}
	return features, nil
}

// runInfraPicker shows per-screen interactive multi-selects for infrastructure adapters.
// Each category gets its own TUI screen.
func runInfraPicker() (initialize.InfrastructureSelection, error) {
	infra := initialize.InfrastructureSelection{}
	screens := []pickerScreen{
		{
			Title: "Cache Backend",
			Items: []tui.PickerItem{
				{Name: "Redis Cache", Description: "Redis-backed caching (recommended)", Selected: true},
			},
			Apply: map[string][]*bool{
				"Redis Cache": {&infra.HasRedis},
			},
		},
		{
			Title: "Event Bus Backend",
			Items: []tui.PickerItem{
				{Name: "Redis Streams", Description: "Durable event bus via Redis Streams", Selected: true},
			},
			Apply: map[string][]*bool{
				// Redis Streams requires a Redis connection.
				"Redis Streams": {&infra.HasRedisStreams, &infra.HasRedis},
			},
		},
		{
			Title: "File Storage",
			Items: []tui.PickerItem{
				{Name: "Disk Storage", Description: "Local filesystem storage", Selected: true},
				{Name: "GCS", Description: "Google Cloud Storage", Selected: true},
				{Name: "S3", Description: "AWS S3 / compatible object storage", Selected: false},
			},
			Apply: map[string][]*bool{
				"Disk Storage": {&infra.HasStorageDisk},
				"GCS":          {&infra.HasStorageGCS},
				"S3":           {&infra.HasStorageS3},
			},
		},
		{
			Title: "Email Delivery",
			Items: []tui.PickerItem{
				{Name: "SendGrid", Description: "Production email delivery via SendGrid", Selected: true},
			},
			Apply: map[string][]*bool{
				"SendGrid": {&infra.HasSendGrid},
			},
		},
		{
			Title: "Telemetry",
			Items: []tui.PickerItem{
				{Name: "Jaeger", Description: "Distributed tracing via OpenTelemetry + Jaeger", Selected: true},
			},
			Apply: map[string][]*bool{
				"Jaeger": {&infra.HasTelemetry},
			},
		},
	}
	if err := runPickerScreens(screens); err != nil {
		return initialize.DefaultInfrastructure(), err
	}
	return infra, nil
}

func runAICompanionPicker() (initialize.AICompanionSelection, error) {
	var ai initialize.AICompanionSelection
	screens := []pickerScreen{
		{
			Title: "AI Companion",
			Items: []tui.PickerItem{
				{Name: "Claude", Description: "CLAUDE.md project config and .claude/skills for common workflows", Selected: true},
			},
			Apply: map[string][]*bool{
				"Claude": {&ai.Claude},
			},
		},
	}
	if err := runPickerScreens(screens); err != nil {
		return initialize.AICompanionSelection{}, err
	}
	return ai, nil
}
