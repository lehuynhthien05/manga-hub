.PHONY: build run-catalog run-backend run-tui test race cover proto tidy fmt vet reset-data evidence progress
build:
	go build -o bin/catalog ./cmd/catalog
	go build -o bin/backend ./cmd/backend
	go build -o bin/tui     ./cmd/tui
run-catalog: ; go run ./cmd/catalog
run-backend: ; go run ./cmd/backend
run-tui:     ; go run ./cmd/tui
test: ; go test ./...
race: ; go test -race ./...
cover: ; go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -n 1
proto: ; protoc -I proto --go_out=. --go_opt=module=mangahub --go-grpc_out=. --go-grpc_opt=module=mangahub proto/manga.proto
tidy: ; go mod tidy
fmt:  ; gofmt -l -w .
vet:  ; go vet ./...
reset-data: ; rm -f var/*.db var/*.db-wal var/*.db-shm
evidence: ; mkdir -p docs/evidence && go test -race -count=1 -v ./... 2>&1 | tee docs/evidence/go-test-$$(date +%Y%m%d-%H%M).txt
progress: ; @awk '/^## /{p=$$0; if(!(p in s)){s[p]=1;o[++n]=p}} /^[[:space:]]*- \[[ xX]\]/{t[p]++} /^[[:space:]]*- \[[xX]\]/{d[p]++} END{for(i=1;i<=n;i++){k=o[i]; if(t[k]) printf "%3d/%-3d %3d%%  %s\n", d[k], t[k], 100*d[k]/t[k], k}}' docs/phases/plan.md
