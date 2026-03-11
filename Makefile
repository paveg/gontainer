CONTAINER := gontainer-dev
BINARY := /app/gontainer

.PHONY: build run shell setup-cgroup

build:
	docker exec $(CONTAINER) go build -o $(BINARY) .

run: build
	docker exec --privileged -it $(CONTAINER) $(BINARY) run /bin/sh

shell:
	docker exec --privileged -it $(CONTAINER) /bin/bash

setup-cgroup:
	docker exec $(CONTAINER) bash -c '\
		mkdir -p /sys/fs/cgroup/init && \
		for pid in $$(cat /sys/fs/cgroup/cgroup.procs); do \
			echo $$pid > /sys/fs/cgroup/init/cgroup.procs 2>/dev/null; \
		done; \
		echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control'
