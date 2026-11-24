// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import "testing"

func TestClassifyUse(t *testing.T) {
	for _, tc := range []struct {
		line, name  string
		method      bool
		lang        string
		found, call bool
	}{
		// Go: calls through a field, method values, declarations.
		{"\tres := p.pipeline.RunLLMPreHooks(ctx, req)", "RunLLMPreHooks", true, "go", true, true},
		{"\tgo bifrost.requestWorker(provider, pq)", "requestWorker", true, "go", true, true},
		{`	r.POST("/v1/chat", lib.ChainMiddlewares(h.chatCompletion, mw...))`, "chatCompletion", true, "go", true, false},
		{"func (p *PluginPipeline) RunLLMPreHooks(ctx *Ctx) error {", "RunLLMPreHooks", true, "go", false, false},
		{"\tRunLLMPreHooks(ctx *Ctx) error", "RunLLMPreHooks", true, "go", false, false}, // interface method
		{"func applyDiscount(total int) int {", "applyDiscount", false, "go", false, false},
		{"\tapplyDiscount := 3", "applyDiscount", false, "go", false, false},
		{"\tHandler: applyDiscount,", "applyDiscount", false, "go", true, false},
		{"\tx := 1 // applyDiscount(x) later", "applyDiscount", false, "go", false, false},
		{`	log.Info("calling applyDiscount(x)")`, "applyDiscount", false, "go", false, false},
		{"\tapplyDiscountAll(x)", "applyDiscount", false, "go", false, false},
		// Python: comments, imports, keyword arguments, strings.
		{"    return pricing.apply_discount(sum(cart))", "apply_discount", false, "python", true, true},
		{"from app.pricing import apply_discount", "apply_discount", false, "python", false, false},
		{"    # apply_discount(cart)", "apply_discount", false, "python", false, false},
		{"    run(apply_discount=True)", "apply_discount", false, "python", false, false},
		{"    x = 'apply_discount(1)'", "apply_discount", false, "python", false, false},
		{"    hooks.append(apply_discount)  # register", "apply_discount", false, "python", true, false},
		{"def apply_discount(total):", "apply_discount", false, "python", false, false},
		// Rust: a lifetime's quote is not a string.
		{"fn total<'a>(x: &'a Cart) -> u32 { cart::apply_discount(x) }", "apply_discount", false, "rust", true, true},
	} {
		found, call := classifyUse(tc.line, tc.name, tc.method, tc.lang)
		if found != tc.found || call != tc.call {
			t.Errorf("classifyUse(%q, %q) = found %v call %v, want %v %v", tc.line, tc.name, found, call, tc.found, tc.call)
		}
	}
}

func TestNameIsSpecific(t *testing.T) {
	for name, want := range map[string]bool{
		"RunLLMPreHooks": true, "apply_discount": true, "requestWorker": true,
		"Close": false, "run": false, "charge": false, "__init__": false, "getX": false, "_private": false,
	} {
		if got := nameIsSpecific(name); got != want {
			t.Errorf("nameIsSpecific(%q) = %v, want %v", name, got, want)
		}
	}
}
