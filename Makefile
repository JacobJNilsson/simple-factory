# The anti-slop-go package patterns. The default covers the whole module.
ANTISLOP_PKGS ?= ./...

.PHONY: check fmt-check neutral neutral-test vet lint antislop test hooks

check: fmt-check neutral-test neutral vet lint antislop test

# The hook runs after staging, so this target reports unformatted files and never rewrites them.
fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt: these files need formatting. Run gofmt -w on them:" >&2; \
		echo "$$out" >&2; \
		exit 1; \
	fi

# The neutrality check fails on a private project name in a file name, staged text, or a commit message.
neutral:
	sh scripts/check-neutral.sh

neutral-test:
	sh scripts/check-neutral-test.sh

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# anti-slop-go v1.4.1 requires Go 1.26 or newer. GOTOOLCHAIN=auto downloads a newer toolchain.
# GOTOOLCHAIN=local on a host older than Go 1.26 fails.
antislop:
	go run github.com/JacobJNilsson/anti-slop-go/cmd/antislop@v1.4.1 $(ANTISLOP_PKGS)

test:
	go test -race ./...

hooks:
	git config core.hooksPath .githooks
