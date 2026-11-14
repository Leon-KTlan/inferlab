.PHONY: api-run api-test web-install web-check web-build check

api-run:
	cd services/api && mkdir -p data && go run ./cmd/server

api-test:
	cd services/api && go test ./...

web-install:
	cd apps/web && npm install

web-check:
	cd apps/web && npm run check

web-build:
	cd apps/web && npm run build

check: api-test web-check web-build
