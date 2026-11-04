# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0

.PHONY: default test lint build

default: test-rust

test:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) test-rust

lint:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) lint-rust

build:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) parsers

%:
	$(MAKE) -C .. CALLER_DIR=$(CURDIR) $@
