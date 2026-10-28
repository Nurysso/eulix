// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"eulix/internal/config"
	"testing"
)

func TestRemoteModeClearsLocalURL(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := llmModeStep().Apply("Remote", cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Local || cfg.LLM.BaseURL != "" {
		t.Error("remote mode retains local endpoint")
	}
}
