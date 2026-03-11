CONTAINER := gontainer-dev
BINARY := /app/gontainer

.PHONY: build run shell

build:
	docker exec $(CONTAINER) go build -o $(BINARY) .

run: build
	docker exec --privileged -it $(CONTAINER) $(BINARY) run /bin/bash

shell:
	docker exec --privileged -it $(CONTAINER) /bin/bash
