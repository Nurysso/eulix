.PHONY: default test lint build

default: test-go

test:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) test-go

lint:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) lint-go

build:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) build-single

%:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) $@
