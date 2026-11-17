.PHONY: build package test check
build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o bin/docker-machine-driver-clo ./cmd/docker-machine-driver-clo
package: build
	mkdir -p dist
	tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf dist/clo-linux-amd64.tar -C bin docker-machine-driver-clo
	gzip -n -f dist/clo-linux-amd64.tar
	cp LICENSE NOTICE THIRD_PARTY_NOTICES.txt dist/
	cd dist && sha256sum clo-linux-amd64.tar.gz LICENSE NOTICE THIRD_PARTY_NOTICES.txt > SHA256SUMS
test:
	go test ./...
check:
	go test -race ./...
	go vet ./...
