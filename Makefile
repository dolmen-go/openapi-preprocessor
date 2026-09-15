
binary = openapi-preprocessor
man = man/man1/$(binary).1
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
	$(go) build -o $@

.PHONY: upgrade-jsonptr

upgrade-jsonptr: ../jsonptr/Makefile
	$(shell $(MAKE) -C ../jsonptr go-get)
	$(go) mod tidy

$(man): main.go
	go generate

install:
	$(go) install

test:
	$(go) test -v ./...

cover:
	$(go) test -coverprofile .coverage.out -covermode=atomic
	$(go) tool cover -html=.coverage.out

.PHONY: man

man: $(man)
	@man -l $(man)
