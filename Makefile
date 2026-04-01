.PHONY: test
test: ## Run all tests.
	go test ./...

.PHONY: build
build: ## Build the CLI binary.
	go build -o bin/gopernicus .

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-28s %s\n", $$1, $$2}'
