import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("vizeulize.html", import.meta.url), "utf8")
  .split("\n")
  .filter((l) => /^const (K|A|nest|pid)=/.test(l))
  .join("\n");
const pid = new Function(`${src};return pid`)();

test("nested functions are labelled by their own name", () => {
  assert.deepEqual(pid("func_outer.inner::a.rs"), ["func", "inner", "a.rs"]);
  assert.equal(pid("func_a.b.c@7::a.py")[1], "c");
  assert.equal(pid("method_PrinterPath_as_path.imp@42::u.rs")[1], "imp");
});

test("ids without nesting keep their labels", () => {
  assert.equal(pid("func_foo::a.go")[1], "foo");
  assert.equal(pid("method_Router_handle::r.go")[1], "Router_handle");
  assert.equal(pid("method_com.foo.Bar_baz::B.java")[1], "com.foo.Bar_baz");
  assert.equal(pid("class_com.foo.Bar::B.java")[1], "com.foo.Bar");
  assert.equal(pid("method_nounderscore")[1], "nounderscore");
  assert.equal(pid("plain")[1], "plain");
});
