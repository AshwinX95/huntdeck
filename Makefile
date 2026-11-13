VERSION ?= dev
LDFLAGS := -s -w -X github.com/mdpsec/huntdeck/internal/version.Version=$(VERSION)
GOLANGCI_LINT ?= golangci-lint
# e.g. DOCKER_BUILD_FLAGS=--network=host on hosts without a docker0 bridge
DOCKER_BUILD_FLAGS ?=

.PHONY: build test lint vuln check docker release-image run fmt tidy clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/huntdeck ./cmd/huntdeck

test:
	go test -race ./...

lint:
	go vet ./...
	$(GOLANGCI_LINT) run

vuln:
	govulncheck ./...

# Every check a change must pass, run locally (there's no hosted CI).
check: lint test vuln

docker:
	docker build $(DOCKER_BUILD_FLAGS) --build-arg VERSION=$(VERSION) -t huntdeck:dev .

# The release image for amd64 and arm64, with an SBOM and provenance
# attached, pushed to IMAGE as VERSION and latest. Run from a clean
# checkout of the tag being released. Needs `docker login ghcr.io` and,
# once per host, arm64 emulation for the Debian layer:
#   docker run --privileged --rm tonistiigi/binfmt --install arm64
IMAGE ?= ghcr.io/mdpsec/huntdeck
PLATFORMS ?= linux/amd64,linux/arm64
release-image:
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "VERSION=vX.Y.Z, the tag being released" >&2; exit 1;; esac
	@test -z "$$(git status --porcelain)" || { echo "commit or stash first: the image is built from the checkout" >&2; exit 1; }
	docker buildx inspect huntdeck-release >/dev/null 2>&1 || docker buildx create --name huntdeck-release --driver docker-container
	docker buildx build --builder huntdeck-release $(DOCKER_BUILD_FLAGS) --platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) --sbom=true --provenance=mode=max \
		--label org.opencontainers.image.source=https://github.com/mdpsec/huntdeck \
		--label org.opencontainers.image.version=$(VERSION) \
		--label org.opencontainers.image.licenses=FSL-1.1-ALv2 \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest --push .

run: build
	HUNTDECK_DATA_DIR=./data HUNTDECK_LISTEN=127.0.0.1:8080 ./bin/huntdeck serve

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
