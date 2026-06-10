package generators

// stackWiring holds the store/repo wiring shared by the generated test
// stacks (integration, e2e, security): package names, import paths, and the
// migrations dir of the hosting database.
type stackWiring struct {
	RepoPkg       string
	RepoImport    string
	StorePkg      string
	StoreImport   string
	MigrationsDir string // e.g. "workshop/migrations/primary"
}

// buildStackWiring computes the wiring for an entity hosted by dbName.
// specMode selects the dialect-neutral spec store package over the pgx one.
func buildStackWiring(resolved *ResolvedFile, modulePath, dbName string, specMode bool) stackWiring {
	storeSuffix := "pgx"
	if specMode {
		storeSuffix = specStorePackageSuffix
	}
	storePkg := StorePackage(resolved.TableName, storeSuffix)
	return stackWiring{
		RepoPkg:       resolved.PackageName,
		RepoImport:    RepoImportPath(modulePath, resolved.DomainName, resolved.PackageName),
		StorePkg:      storePkg,
		StoreImport:   StoreImportPath(modulePath, resolved.DomainName, resolved.PackageName, storePkg),
		MigrationsDir: "workshop/migrations/" + dbName,
	}
}
