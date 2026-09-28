.PHONY: all backend frontend run test clean

frontend-deps:
	cd frontend && npm install

frontend:
	cd frontend && npx tsc --noEmit && npx vite build

backend:
	go build -o bin/server ./cmd/server

all: frontend backend

test:
	go test ./...
	cd frontend && npx tsc --noEmit

run: all
	./bin/server -addr :8080 -data ./data

dev-backend:
	go run ./cmd/server -addr :8080 -data ./data

dev-frontend:
	cd frontend && npx vite
