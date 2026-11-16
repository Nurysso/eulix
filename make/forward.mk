# Forwards every goal given in a sub-directory to the root Makefile in ONE
# call, so multi-word goals like `make install lsp` work from anywhere.
#
# A sub-directory Makefile sets, before including this file:
#   ALIAS_default := <root target for a bare `make`>   (default: help)
#   ALIAS_<goal>  := <root target>                       (optional renames)

FORWARD_ROOT := $(patsubst %/,%,$(dir $(lastword $(MAKEFILE_LIST))))/..
ALIAS_default ?= help

FORWARD_GOALS := $(if $(MAKECMDGOALS),$(MAKECMDGOALS),default)
FORWARD_TARGETS := $(foreach g,$(FORWARD_GOALS),$(or $(ALIAS_$(g)),$(g)))

.DEFAULT_GOAL := default
.PHONY: _forward

_forward:
	@$(MAKE) --no-print-directory -C $(FORWARD_ROOT) CALLER_DIR=$(CURDIR) $(FORWARD_TARGETS)

# Never try to remake the makefiles themselves through the catch-all below.
$(MAKEFILE_LIST): ;

default: _forward ; @:
%: _forward ; @:
