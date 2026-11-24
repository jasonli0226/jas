# VERSION names local builds after the checkout (e.g. v1.2.0-91-g116b401).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
MAX_SIZE := 15728640

.PHONY: build install chat test perf bench size manual claude-setup

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/jas ./cmd/jas

# Puts the current build on PATH as `jas` (in $(go env GOBIN) or ~/go/bin).
install:
	CGO_ENABLED=0 go install -trimpath -ldflags="$(LDFLAGS)" ./cmd/jas

# Rebuilds and opens the inline chat on the current code. DIR picks the
# project to chat in (default: this repo); ARGS passes flags, e.g.
#   make chat ARGS=--continue    make chat DIR=~/some/project
chat: build
	cd $(or $(DIR),.) && $(CURDIR)/bin/jas $(ARGS)

# Copies docs/manual.md into the built-in jas skill (a test checks they match).
manual:
	cp docs/manual.md cmd/jas/manual/jas/references/manual.md

test:
	go vet ./...
	go test ./...
	bash .agents/skills/pr-review-loop/scripts/link-claude-code_test.sh
	bash .agents/skills/pr-review-loop/scripts/install-reviewer_test.sh
	bash .agents/skills/pr-review-loop/scripts/review-pr_test.sh

# Links the pr-review-loop skill into .claude/skills (.claude/ is gitignored;
# once per clone).
claude-setup:
	bash .agents/skills/pr-review-loop/scripts/link-claude-code.sh

perf:
	JAS_PERF_TESTS=1 go test -run Version ./cmd/jas/

# Needs OPENROUTER_API_KEY. BENCH_AGENT=pi compares against pi.
bench: build
	bench/coldstart.sh bin/jas
	bench/run.sh $(or $(BENCH_AGENT),jas)

size: build
	@sz=$$(wc -c < bin/jas); echo "bin/jas: $$sz bytes"; \
	if [ "$$sz" -ge $(MAX_SIZE) ]; then echo "binary >= 15MB" >&2; exit 1; fi
