FRAMEWORK_SRC     := ../gopernicus/core/repositories
BRIDGE_SRC        := ../gopernicus/bridge/repositories
FRAMEWORK_DST     := internal/frameworkrepos

# Bootstrap file patterns to sync from each entity directory.
# queries.sql is always synced. .go files are synced only when hand-written
# (i.e. bootstrap files like repository.go, store.go — not generated files).
# .go files are stored as .go.tmpl in the embed dir so the Go compiler
# doesn't try to compile them. boot repos strips .tmpl on write.
SYNC_SQL_FILES  := queries.sql
SYNC_GO_FILES   := repository.go cache.go fop.go
SYNC_PGX_FILES  := store.go fop.go
SYNC_YML_FILES  := bridge.yml
SYNC_BRIDGE_GO  := bridge.go routes.go http.go fop.go

.PHONY: sync-framework-repos
sync-framework-repos: ## Sync canonical bootstrap files from the framework module into the CLI embed.
	@echo "Syncing framework repos from $(FRAMEWORK_SRC) ..."
	@for domain in auth rebac tenancy events; do \
		find "$(FRAMEWORK_SRC)/$$domain" -mindepth 1 -maxdepth 1 -type d | while read entitydir; do \
			entity=$$(basename $$entitydir); \
			dst="$(FRAMEWORK_DST)/$$domain/$$entity"; \
			mkdir -p "$$dst"; \
			for f in $(SYNC_SQL_FILES); do \
				if [ -f "$$entitydir/$$f" ]; then \
					cp "$$entitydir/$$f" "$$dst/$$f"; \
					echo "  synced $$domain/$$entity/$$f"; \
				fi; \
			done; \
			for f in $(SYNC_GO_FILES); do \
				if [ -f "$$entitydir/$$f" ]; then \
					cp "$$entitydir/$$f" "$$dst/$${f}.tmpl"; \
					echo "  synced $$domain/$$entity/$${f}.tmpl"; \
				fi; \
			done; \
			pgxdir="$$entitydir/$${entity}pgx"; \
			if [ -d "$$pgxdir" ]; then \
				mkdir -p "$$dst/$${entity}pgx"; \
				for f in $(SYNC_PGX_FILES); do \
					if [ -f "$$pgxdir/$$f" ]; then \
						cp "$$pgxdir/$$f" "$$dst/$${entity}pgx/$${f}.tmpl"; \
						echo "  synced $$domain/$$entity/$${entity}pgx/$${f}.tmpl"; \
					fi; \
				done; \
			fi; \
		done; \
	done
	@echo ""
	@echo "Syncing bridge repos from $(BRIDGE_SRC) ..."
	@for domain in auth rebac tenancy events; do \
		bridgedir="$(BRIDGE_SRC)/$${domain}reposbridge"; \
		if [ -d "$$bridgedir" ]; then \
			find "$$bridgedir" -mindepth 1 -maxdepth 1 -type d | while read entitybridgedir; do \
				bridgepkg=$$(basename $$entitybridgedir); \
				entity=$${bridgepkg%bridge}; \
				dst="$(FRAMEWORK_DST)/$$domain/$$entity/bridge"; \
				mkdir -p "$$dst"; \
				for f in $(SYNC_YML_FILES); do \
					if [ -f "$$entitybridgedir/$$f" ]; then \
						cp "$$entitybridgedir/$$f" "$$dst/$$f"; \
						echo "  synced $$domain/$$entity/bridge/$$f"; \
					fi; \
				done; \
				for f in $(SYNC_BRIDGE_GO); do \
					if [ -f "$$entitybridgedir/$$f" ]; then \
						cp "$$entitybridgedir/$$f" "$$dst/$${f}.tmpl"; \
						echo "  synced $$domain/$$entity/bridge/$${f}.tmpl"; \
					fi; \
				done; \
			done; \
		fi; \
	done
	@echo "Done."

.PHONY: test
test: ## Run all tests.
	go test ./...

.PHONY: build
build: ## Build the CLI binary.
	go build -o bin/gopernicus .

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-28s %s\n", $$1, $$2}'
