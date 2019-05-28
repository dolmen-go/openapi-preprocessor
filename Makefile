
binary = openapi-preprocessor
go = GO111MODULE=on go
version = $$(git describe --tags --always --dirty)

all: $(binary)

.PHONY: all test clean install .FORCE

clean:
	rm -f $(binary)

version:
	@echo "$(version)"

$(binary): .FORCE
	@printf 'version: \033[1;33m%s\033[m\n' $(version)
	$(go) build -ldflags "-X main.version=@(#)$(version)" -o $@

install:
	$(go) install -ldflags "-X main.version=@(#)$(version)"

test:
	$(go) test -v ./...

cover:
	$(go) test -coverprofile .coverage.out -covermode=atomic
	$(go) tool cover -html=.coverage.out

