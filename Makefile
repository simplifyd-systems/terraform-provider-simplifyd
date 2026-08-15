BINARY  := terraform-provider-simplifyd
VERSION ?= dev

.PHONY: build install test testacc docs lint clean

build:
	go build -ldflags="-X main.version=$(VERSION)" -o $(BINARY) .

# Installs into the local plugin cache so `terraform` picks it up via a
# dev_overrides block in ~/.terraformrc (see README).
install: build
	mkdir -p ~/go/bin && cp $(BINARY) ~/go/bin/

test:
	go test ./... -v

# Acceptance tests hit a real API. Point them at staging, never production.
testacc:
	TF_ACC=1 go test ./... -v -timeout 30m

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name simplifyd

lint:
	golangci-lint run

clean:
	rm -f $(BINARY)
