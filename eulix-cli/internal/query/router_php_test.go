// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import (
	"path/filepath"
	"testing"
)

func TestExtractSignaturePHP(t *testing.T) {
	cases := []struct {
		name, source, want string
		start              int
	}{
		{
			name:   "modifiers and return type",
			source: "<?php\nfinal public static function lookup(int $id): ?Record { return null; }\n",
			start:  2,
			want:   "  final public static function lookup(int $id): ?Record\n",
		},
		{
			name:   "abstract declaration stops before next method",
			source: "<?php\nabstract protected function lookup(\n    int $id,\n    array $options = [],\n): Record|null;\npublic function unrelated(): void {}\n",
			start:  2,
			want:   "  abstract protected function lookup(\n      int $id,\n      array $options = [],\n  ): Record|null;\n",
		},
		{
			name:   "interface declaration stops on same line",
			source: "<?php\ninterface Store {\npublic function &lookup(int $id): (Readable&Writable)|null; public function other(): void;\n}\n",
			start:  3,
			want:   "  public function &lookup(int $id): (Readable&Writable)|null;\n",
		},
		{
			name:   "attributes and parameter attributes",
			source: "<?php\n#[Route(\n    path: '/items/{id}',\n    options: ['marker' => '];{'],\n)]\n#[Deprecated]\npublic function lookup(#[SensitiveParameter] string $key): ?Record\n{\n    return null;\n}\n",
			start:  2,
			want:   "  #[Route(\n      path: '/items/{id}',\n      options: ['marker' => '];{'],\n  )]\n  #[Deprecated]\n  public function lookup(#[SensitiveParameter] string $key): ?Record\n",
		},
		{
			name:   "quoted defaults do not change delimiter depth",
			source: "<?php\n" + `function lookup(string $label = ');{', array $options = ['key' => "[\"{]"]): array { return []; }` + "\n",
			start:  2,
			want:   `  function lookup(string $label = ');{', array $options = ['key' => "[\"{]"]): array` + "\n",
		},
		{
			name:   "comments do not terminate the declaration",
			source: "<?php\npublic function lookup( // ); {\n    int $id, # ); {\n    /* ]; {\n       ) */ string $label = 'x',\n): Record /* {; */ { return new Record(); }\n",
			start:  2,
			want:   "  public function lookup( // ); {\n      int $id, # ); {\n      /* ]; {\n         ) */ string $label = 'x',\n  ): Record /* {; */\n",
		},
	}
	for _, ext := range []string{".php", ".phtml", ".inc", ".PHP", ".PHTML", ".INC"} {
		for _, tc := range cases {
			t.Run(ext+"/"+tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "store"+ext)
				writeFile(t, path, tc.source)
				got, err := extractSignature(path, tc.start)
				if err != nil || got != tc.want {
					t.Errorf("extractSignature() = (%q, %v), want %q", got, err, tc.want)
				}
			})
		}
	}
}

func TestExtractSignatureIncWithoutPHPTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "declarations.inc")
	// The extension is also used by C/assembly; retain the generic reader.
	writeFile(t, path, "extern int lookup(void);\nint other(void) { return 1; }\n")
	got, err := extractSignature(path, 1)
	want := "  extern int lookup(void);\n  int other(void)\n"
	if err != nil || got != want {
		t.Errorf("extractSignature() = (%q, %v), want %q", got, err, want)
	}
}

func TestPHPDependencyFileIntent(t *testing.T) {
	for _, path := range []string{"views/item.phtml", "includes/store.inc"} {
		if got := classifyDepIntent("show me "+path, path); got != depIntentFile {
			t.Errorf("classifyDepIntent(%q) = %v, want file intent", path, got)
		}
	}
}

func TestPHPFilePathExtraction(t *testing.T) {
	for _, path := range []string{"view.phtml", "store.inc"} {
		if got := extractFilePath("explain " + path); got != path {
			t.Errorf("extractFilePath(%q) = %q, want %q", path, got, path)
		}
	}
}
