.PHONY: build test integration smoke run up down

build:
	go build -o bin/clipforge ./cmd/clipforge
	cd web && pnpm install --frozen-lockfile && pnpm build

test:
	go test -race ./...
	go vet ./...

integration:
	test -n "$(TEST_DATABASE_URL)"
	MEDIA_TEST=1 go test -race -count=1 ./...

smoke:
	bash scripts/smoke.sh

run:
	go run ./cmd/clipforge

up:
	docker compose up --build -d

down:
	docker compose down
