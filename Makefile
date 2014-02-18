REV=$(shell git log -n 1 --format='%h - %aD')
VERSION=version.go
all: governator
governator: *.go
	@echo "package main\nfunc init() { governatorVersion = \"$(REV)\" }" | gofmt > $(VERSION)
	go build
	@rm -f $(VERSION)

clean:
	go clean
