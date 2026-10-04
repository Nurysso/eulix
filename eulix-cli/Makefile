.PHONY: default test lint build

default: test-go

test:
	$(MAKE) -C .. test-go

lint:
	$(MAKE) -C .. lint-go

build:
	$(MAKE) -C .. build-single

%:
	$(MAKE) -C .. $@
