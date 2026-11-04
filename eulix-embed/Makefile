.PHONY: default test lint build

default: test-python

test:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) test-python

lint:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) lint-python

build:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) embed-zip

%:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) $@
