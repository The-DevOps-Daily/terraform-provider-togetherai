BINARY = terraform-provider-togetherai

default: build

# Build the provider binary.
build:
	go build -o $(BINARY)

# Install the provider into GOBIN for manual testing.
install:
	go install

# Unit tests and the mock acceptance tests (real terraform against an
# in-process mock API). No key, no network. The real-API TestAcc* tests skip
# without TOGETHER_API_KEY.
test:
	TF_ACC=1 go test ./... -count=1 -timeout 600s

# Acceptance tests against the real Together AI API. They create and destroy
# real resources and need TOGETHER_API_KEY. Tests that cost money also need
# TOGETHER_ACC_EXPENSIVE=1. See the README for the cost of each test.
testacc:
	TF_ACC=1 go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 120m

# terraform validate every example; plan them against the mock API.
examples:
	MOCK=1 sh scripts/check-examples.sh

# Run the mock API on a local port.
mock:
	go run ./tools/mockserver

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...

# Regenerate docs/ from the schema descriptions and examples/.
docs:
	go tool tfplugindocs generate --provider-name togetherai

.PHONY: default build install test testacc examples mock fmt vet lint docs
