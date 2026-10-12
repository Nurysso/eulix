# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0

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
