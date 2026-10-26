# Developer container for hq: Go 1.27 with the module cache warmed up.
FROM golang:1.27

WORKDIR /workspace

# Pre-download the dependencies pinned by go.sum so that builds work
# without re-fetching them.
COPY go.mod go.sum ./
RUN go mod download
