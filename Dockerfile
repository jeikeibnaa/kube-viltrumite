# syntax=docker/dockerfile:1
#
# Operator image: React UI + static Go binary on distroless.
#   make docker-build            (docker build -t ghcr.io/jeikeibnaa/kube-viltrumite:dev .)
#
# The build stages run on the build machine's platform (--platform=$BUILDPLATFORM)
# and Go cross-compiles for TARGETOS/TARGETARCH, so multi-arch builds need no
# emulation.

# ---- UI: build the React dashboard into ui/dist ----
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# ---- Operator: static binary; the knowledge base is embedded via go:embed ----
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY knowledge/ knowledge/
# Set by BuildKit from --platform; empty values fall back to the builder's linux/arch.
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/operator ./cmd/operator

# ---- Runtime: no shell, no package manager, non-root ----
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/operator /operator
COPY --from=ui /src/ui/dist /ui
# Numeric so Kubernetes can enforce runAsNonRoot.
USER 65532:65532
ENTRYPOINT ["/operator", "--ui-path=/ui"]
