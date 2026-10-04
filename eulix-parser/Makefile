.PHONY: default test lint build

default: test-rust

test:
	$(MAKE) -C .. test-rust

lint:
	$(MAKE) -C .. lint-rust

build:
	$(MAKE) -C .. parsers

%:
	$(MAKE) -C .. $@
