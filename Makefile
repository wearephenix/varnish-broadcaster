GO_VERSION = 1.17.1

UID = $(shell id -u)
GID = $(shell id -g)

fmt:
	docker run -it -e CGO_ENABLED=0 -v $(shell pwd):/code -w /code golang:${GO_VERSION} go fmt ./...

build:
	rm -rf ./dist/varnish-broadcaster
	docker run -it -e CGO_ENABLED=0 -v $(shell pwd):/code -w /code golang:${GO_VERSION} /bin/bash -c 'go build -o dist/varnish-broadcaster && chown -R ${UID}:${GID} dist'
