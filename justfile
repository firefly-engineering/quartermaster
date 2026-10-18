# quartermaster — run `just` for the recipe list.

default:
    @just --list

build:
    go build -o bin/worklog ./cmd/worklog

test:
    gotestsum -- -race ./...

fmt:
    gofmt -s -w .
    nix fmt

fmt-check:
    test -z "$(gofmt -s -l .)"
    nix fmt -- --ci

lint:
    golangci-lint run ./...

# What CI runs, minus `nix flake check`.
check: fmt-check lint test

flake-check:
    nix flake check --quiet
