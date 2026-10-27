db-up:
	docker compose up -d db db-test
db-down:
	docker compose down
run:
	set -a && . ./.env && set +a && go run ./cmd/api
test:
	go test -p 1 ./...
vet:
	go vet ./...
