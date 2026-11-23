import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The worker's source, as the page runs it.
const html = readFileSync(new URL("vizeulize.html", import.meta.url), "utf8");
const start = html.indexOf('<script id="wk" type="text/plain">');
const wk = html.slice(html.indexOf(">", start) + 1, html.indexOf("</script>", start));
const posted = [];
const W = new Function("postMessage", `${wk};return{isFB,decodeFB,onmessage}`)((m) => posted.push(m));

// Written by eulix_parser --emit-json; the .json is its copy of the .fb.
const fixture = new URL("../eulix-cli/internal/kbfile/testdata/", import.meta.url);
const fbBytes = () => {
  const b = readFileSync(new URL("kb_call_graph.fb", fixture));
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
};
const copy = JSON.parse(readFileSync(new URL("kb_call_graph.json", fixture), "utf8"));

test("kb_call_graph.fb decodes to what the parser's JSON copy shows", () => {
  const buf = fbBytes();
  assert.ok(W.isFB(buf));
  const d = W.decodeFB(buf);
  assert.deepEqual(
    d.nodes,
    copy.nodes.map(({ id, node_type, file }) => ({ id, node_type, file })),
  );
  assert.deepEqual(
    d.edges,
    copy.edges.map(({ from, to, edge_type, conditional }) => ({ from, to, edge_type, conditional })),
  );
  assert.deepEqual(d.tags, Object.fromEntries(copy.tags.map((t) => [t.key, t.values])));
  assert.deepEqual(
    d.file_categories,
    Object.fromEntries(copy.file_categories.map((c) => [c.key, c.category])),
  );
});

test("the .fb file and its JSON copy render the same graph", () => {
  posted.length = 0;
  W.onmessage({ data: { id: 1, buf: fbBytes() } });
  W.onmessage({ data: { id: 2, buf: new TextEncoder().encode(JSON.stringify(copy)).buffer } });
  const [a, b] = posted;
  assert.equal(a.kind, "graph");
  assert.equal(a.perf.format, "fb");
  assert.equal(b.perf.format, "json");
  for (const k of ["bytes", "decodeMs", "buildMs"]) assert.ok(a.perf[k] >= 0, `perf.${k}`);
  // timings and format differ by design; the graph itself must not
  assert.deepEqual({ ...a, id: 0, perf: 0 }, { ...b, id: 0, perf: 0 });
  assert.ok(a.tg.length > 0 && a.catNames.length > 0, "tags and categories reach the graph");
});

test("a call graph from another format version is refused", () => {
  const buf = fbBytes();
  const v = new DataView(buf);
  const root = v.getUint32(0, true);
  const vt = root - v.getInt32(root, true);
  v.setUint32(root + v.getUint16(vt + 4, true), 2, true); // field 0: format_version
  assert.throws(() => W.decodeFB(buf), /format version 2/);
});

test("a truncated file is an error, not a partial graph", () => {
  const buf = fbBytes();
  assert.throws(() => W.decodeFB(buf.slice(0, buf.byteLength >> 1)), RangeError);
  posted.length = 0;
  W.onmessage({ data: { id: 3, buf: buf.slice(0, 40) } });
  assert.equal(posted[0].kind, "error");
});
