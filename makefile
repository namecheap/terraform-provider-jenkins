SHELL := /bin/bash

BINARY        := terraform-provider-jenkins
GOBIN         := $(shell go env GOBIN)
GOBIN         := $(if $(GOBIN),$(GOBIN),$(shell go env GOPATH)/bin)
COMPOSE_FILE  := ./integration/docker-compose.yml

export COMPOSE_FILE

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compile and install binary to GOBIN (prints terraformrc dev_overrides snippet)
	go install
	@echo ""
	@echo "Installed $(BINARY) to $(GOBIN)"
	@echo "Add this to ~/.terraformrc to use the local build:"
	@echo ""
	@echo '  provider_installation {'
	@echo '    dev_overrides {'
	@echo '      "namecheap/jenkins-v2" = "$(GOBIN)"'
	@echo '    }'
	@echo '    direct {}'
	@echo '  }'

.PHONY: generate
generate: ## Regenerate docs and any code-gen artifacts
	cd tools; go generate ./...

.PHONY: test
test: ## Run unit tests
	go test -cover ./...

.PHONY: test-cover
test-cover: ## Run unit tests with a coverage report (used by CI)
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

.PHONY: testacc
testacc: ## Run acceptance tests against a local Docker Jenkins
	@docker compose build
	@docker compose up -d --force-recreate jenkins
	@while [ "$$(docker inspect jenkins-provider-acc --format '{{ .State.Health.Status }}')" != "healthy" ]; do \
		echo "Waiting for Jenkins to start..."; sleep 3; \
	done
	TF_ACC=1 JENKINS_URL="http://localhost:8080" JENKINS_USERNAME="admin" JENKINS_PASSWORD="admin" \
		go test -v -cover ./...
	@docker compose down

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Auto-fix formatting issues
	golangci-lint fmt ./...

.PHONY: docs
docs: ## Alias for generate (for compatibility with CI docs check)
	$(MAKE) generate

.PHONY: docs-validate
docs-validate: ## Validate docs structure with tfplugindocs
	@go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest
	@PATH="$(GOBIN):$$PATH" tfplugindocs validate --provider-name "jenkins"

.PHONY: clean
clean: ## Remove compiled binary
	rm -f "$(GOBIN)/$(BINARY)"