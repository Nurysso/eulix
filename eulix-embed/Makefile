.PHONY: default test lint build

default: test-python

test:
	$(MAKE) -C .. test-python

lint:
	$(MAKE) -C .. lint-python

build:
	$(MAKE) -C .. embed-zip

%:
	$(MAKE) -C .. $@
