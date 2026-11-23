import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The worker's source, as the page runs it.
const html = readFileSync(new URL("vizeulize.html", import.meta.url), "utf8");
const start = html.indexOf('<script id="wk" type="text/plain">');
const wk = html.slice(html.indexOf(">", start) + 1, html.indexOf("</script>", start));
const posted = [];
const W = new Function("postMessage", `${wk};return{onmessage}`)((m) => posted.push(m));

const send = (v) => {
  posted.length = 0;
  const buf = typeof v === "string" ? v : JSON.stringify(v);
  W.onmessage({ data: { id: 7, buf: new TextEncoder().encode(buf).buffer } });
  assert.equal(posted.length, 1);
  return posted[0];
};
const at = (g, id) => g.ids.indexOf(id);
const out = (g, i) => [...g.oE.slice(g.oS[i], g.oS[i + 1])].map((j) => g.ids[g.eT[j]]);
const inn = (g, i) => [...g.iE.slice(g.iS[i], g.iS[i + 1])].map((j) => g.ids[g.eF[j]]);

const kb = {
  nodes: [
    { id: "func_main::a.go", node_type: "function", file: "a.go" },
    { id: "func_helper::a.go", node_type: "function", file: "a.go" },
    { id: "class_Base::b.py", node_type: "class", file: "b.py", name: "Base" },
    { id: "class_Child::b.py", node_type: "class", file: "b.py" },
  ],
  edges: [
    { from: "func_main::a.go", to: "func_helper::a.go", edge_type: "calls" },
    { source: "func_main::a.go", target: "func_printf::fmt", type: "calls", conditional: true },
    { caller: "func_helper::a.go", callee: "func_helper::a.go" },
    { from: "class_Child::b.py", to: "class_Base::b.py", edge_type: "inherits" },
    { from: "func_main::a.go" },
  ],
  tags: { "func_main::a.go": ["exported", "entry"], "func_helper::a.go": [], "func_gone::x": ["t"] },
  file_categories: { "a.go": "source", "b.py": "test" },
};

test("edges build CSR adjacency in both directions", () => {
  const g = send(kb);
  assert.equal(g.kind, "graph");
  assert.equal(g.id, 7);
  assert.equal(g.n, 5);
  assert.equal(g.m, 4, "the edge with no target is dropped");
  const main = at(g, "func_main::a.go");
  const helper = at(g, "func_helper::a.go");
  assert.deepEqual(out(g, main), ["func_helper::a.go", "func_printf::fmt"]);
  assert.deepEqual(inn(g, helper), ["func_main::a.go", "func_helper::a.go"]);
  assert.deepEqual(out(g, helper), ["func_helper::a.go"]);
  assert.equal(g.oS[g.n], g.m);
  assert.equal(g.iS[g.n], g.m);
});

test("edge fields accept source/target and caller/callee, defaulting to calls", () => {
  const g = send(kb);
  assert.deepEqual(g.tn, ["calls", "inherits"]);
  assert.deepEqual([...g.tk], [0, 1]);
  assert.deepEqual([...g.eC], [0, 1, 0, 0]);
});

test("nodes only seen in edges are external", () => {
  const g = send(kb);
  const ext = at(g, "func_printf::fmt");
  assert.equal(g.cat[ext], 6);
  assert.equal(g.names[ext], "printf");
  assert.equal(g.files[ext], "fmt");
  assert.equal(g.cat[at(g, "func_main::a.go")], 0);
  assert.equal(g.cat[at(g, "class_Base::b.py")], 2);
});

test("node fields override what the id says", () => {
  const g = send({ nodes: [{ id: "func_x::a.c", node_type: "Method", name: "X", file: "z.c" }] });
  assert.equal(g.cat[0], 1);
  assert.equal(g.kn[g.kx[0]], "method");
  assert.equal(g.names[0], "X");
  assert.equal(g.files[0], "z.c");
});

test("call counts leave out inheritance", () => {
  const g = send(kb);
  const base = at(g, "class_Base::b.py");
  const child = at(g, "class_Child::b.py");
  assert.equal(g.ci[base], 0);
  assert.equal(g.co[child], 0);
  assert.equal(g.co[at(g, "func_main::a.go")], 2);
  assert.equal(g.ci[at(g, "func_helper::a.go")], 2);
});

test("tags skip empty lists and unknown nodes", () => {
  const g = send(kb);
  assert.deepEqual(g.tagNames, ["exported", "entry"]);
  assert.deepEqual(g.tg, [[at(g, "func_main::a.go"), [0, 1]]]);
});

test("file categories index from 1, 0 meaning none", () => {
  const g = send(kb);
  assert.deepEqual(g.catNames, ["source", "test"]);
  assert.equal(g.fc[at(g, "func_main::a.go")], 1);
  assert.equal(g.fc[at(g, "class_Base::b.py")], 2);
  assert.equal(g.fc[at(g, "func_printf::fmt")], 0);
});

test("sorted {key,...} lists from --emit-json read like maps", () => {
  const a = send(kb);
  const b = send({
    ...kb,
    tags: Object.entries(kb.tags).map(([key, values]) => ({ key, values })),
    file_categories: Object.entries(kb.file_categories).map(([key, category]) => ({ key, category })),
  });
  assert.deepEqual({ ...a, perf: 0 }, { ...b, perf: 0 });
});

test("nodes with no id are skipped", () => {
  const g = send({ nodes: [null, {}, { id: 3 }] });
  assert.deepEqual(g.ids, ["3"]);
  assert.equal(g.cat[0], 6);
});

test("JSON with no nodes or edges is unknown", () => {
  assert.deepEqual(send({ hello: 1 }), { id: 7, kind: "unknown" });
});

test("bad input reports an error instead of throwing", () => {
  const r = send("{not json");
  assert.equal(r.kind, "error");
  assert.ok(r.msg.length > 0);
});
