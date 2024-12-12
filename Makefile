
binary = openapi-preprocessor
man = $(binary).1
go = GO111MODULE=on go
# Go-modules versionning style
version = $(shell TZ=UTC git log -1 '--date=format-local:%Y%m%d%H%M%S' --abbrev=12 '--pretty=tformat:v0.0.0-%cd-%h' go.mod $(shell $(go) list -f '{{$$Dir := .Dir}}{{range .GoFiles}}{{$$Dir}}/{{.}} {{end}}' ./... ))

all: $(binary) $(man)

.PHONY: all test clean install .FORCE

clean:
	rm -f $(binary)

version:
	@echo "$(version)"

$(binary): .FORCE
	@printf 'version: \033[1;33m%s\033[m\n' $(version)
	$(go) build -ldflags "-X main.version=@(#)$(version)" -o $@

.PHONY: upgrade-jsonptr

upgrade-jsonptr: ../jsonptr/Makefile
	$(shell $(MAKE) -C ../jsonptr go-get)
	$(go) mod tidy

$(man): main.go
	go generate

install:
	$(go) install -ldflags "-X main.version=@(#)$(version)"

test:
	$(go) test -v ./...

cover:
	$(go) test -coverprofile .coverage.out -covermode=atomic
	$(go) tool cover -html=.coverage.out

