#!/usr/bin/env python3

# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0

# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


"""End-to-end LSP conformance driver for eulix-lsp.

Spawns the real binary, speaks JSON-RPC over stdio exactly as an editor does,
and asserts on the responses. This is the check that unit tests cannot make:
that the handlers are actually reachable over the wire and that nothing the
server prints lands on stdout.
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import time

BIN = sys.argv[1] if len(sys.argv) > 1 else "./target/release/eulix-lsp"

WORKSPACE = {
    "lib.rs": '''//! Demo library.

/// Adds two numbers together.
///
/// # Example
/// ```
/// assert_eq!(math::add(1, 2), 3);
/// ```
pub fn add(a: i32, b: i32) -> i32 {
    helper(a) + b
}

fn helper(x: i32) -> i32 {
    // TODO: make this faster
    x * 1
}

/// Entry point.
pub fn main() {
    let total = add(1, 2);
    let unused = helper(3);
    println!("{total}");
}
''',
    "app.py": '''def main():
    """Run the app."""
    total = add(1, 2)
    print(total)
''',
    "broken.rs": '''fn oops( {
    let x =
}
''',
    "notes.md": "# not a source file\n",
    "rename.rs": '''fn twice() -> i32 {
    2
}

fn use_twice() -> i32 {
    twice() + twice()
}
''',
}


class Client:
    def __init__(self, proc):
        self.proc = proc
        self.msgs = []
        self.lock = threading.Lock()
        self.stdout_raw = bytearray()
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        buf = b""
        while True:
            chunk = self.proc.stdout.read(1)
            if not chunk:
                return
            self.stdout_raw += chunk
            buf += chunk
            while True:
                head_end = buf.find(b"\r\n\r\n")
                if head_end == -1:
                    break
                header = buf[:head_end].decode("ascii", "replace")
                length = None
                for line in header.split("\r\n"):
                    if line.lower().startswith("content-length:"):
                        length = int(line.split(":", 1)[1].strip())
                if length is None:
                    return
                total = head_end + 4 + length
                if len(buf) < total:
                    break
                body = buf[head_end + 4:total]
                buf = buf[total:]
                try:
                    msg = json.loads(body)
                except json.JSONDecodeError:
                    # Non-protocol bytes on stdout. Recorded, not fatal, so the
                    # final assertion can report precisely what leaked.
                    with self.lock:
                        self.msgs.append({"__unparseable__": body[:200].decode("utf8", "replace")})
                    continue
                with self.lock:
                    self.msgs.append(msg)

    def send(self, obj):
        data = json.dumps(obj).encode("utf-8")
        self.proc.stdin.write(
            f"Content-Length: {len(data)}\r\n\r\n".encode("ascii") + data
        )
        self.proc.stdin.flush()

    def request(self, id, method, params=None):
        # `shutdown` and `exit` take no params; sending `{}` is a spec
        # violation that tower-lsp rejects with -32602.
        msg = {"jsonrpc": "2.0", "id": id, "method": method}
        if params is not None:
            msg["params"] = params
        self.send(msg)

    def notify(self, method, params=None):
        msg = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            msg["params"] = params
        self.send(msg)

    def wait_for_id(self, id, timeout=15.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            with self.lock:
                for m in self.msgs:
                    if m.get("id") == id and ("result" in m or "error" in m):
                        return m
            time.sleep(0.02)
        return None

    def diagnostics_for(self, uri, timeout=15.0, latest=False):
        deadline = time.time() + timeout
        found = None
        while time.time() < deadline:
            with self.lock:
                for m in self.msgs:
                    if m.get("method") == "textDocument/publishDiagnostics" and \
                       m.get("params", {}).get("uri") == uri:
                        found = m["params"].get("diagnostics", [])
                        if not latest:
                            return found
            time.sleep(0.02)
        return found


PASS, FAIL = [], []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(f"  {'PASS' if cond else 'FAIL'}  {name}" + (f"  -- {detail}" if detail and not cond else ""))


def main():
    tmp = tempfile.mkdtemp(prefix="eulix-e2e-")
    for name, content in WORKSPACE.items():
        pathlib.Path(tmp, name).write_text(content, encoding="utf-8")

    env = dict(os.environ, RUST_LOG="warn")
    proc = subprocess.Popen(
        [BIN], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, env=env, bufsize=0,
    )
    c = Client(proc)
    errbuf = bytearray()

    def _drain_err():
        while True:
            b = proc.stderr.read(1)
            if not b:
                return
            errbuf.extend(b)

    threading.Thread(target=_drain_err, daemon=True).start()

    root_uri = pathlib.Path(tmp).as_uri()
    lib_uri = pathlib.Path(tmp, "lib.rs").as_uri()
    app_uri = pathlib.Path(tmp, "app.py").as_uri()
    broken_uri = pathlib.Path(tmp, "broken.rs").as_uri()

    print("\n[1] initialize")
    c.request(1, "initialize", {
        "processId": os.getpid(),
        "rootUri": root_uri,
        "capabilities": {
            "textDocument": {
                "documentSymbol": {"hierarchicalDocumentSymbolSupport": True},
                "semanticTokens": {
                    "requests": {"full": True},
                    "tokenTypes": [], "tokenModifiers": [], "formats": ["relative"],
                },
            }
        },
        "initializationOptions": {"debounceMs": 50, "scanOnStart": True},
    })
    r = c.wait_for_id(1)
    check("initialize returns a result", r and "result" in r, json.dumps(r)[:300] if r else "no response")
    caps = (r or {}).get("result", {}).get("capabilities", {})
    for feat in ["documentSymbolProvider", "hoverProvider", "definitionProvider",
                 "referencesProvider", "completionProvider", "renameProvider",
                 "callHierarchyProvider", "foldingRangeProvider", "semanticTokensProvider"]:
        check(f"advertises {feat}", caps.get(feat) is not None)
    sync = caps.get("textDocumentSync", {})
    check("sync is incremental", sync.get("change") == 2, str(sync.get("change")))

    c.notify("initialized", {})
    time.sleep(2.5)  # let the background scan finish

    print("\n[2] didOpen + documentSymbol")
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": lib_uri, "languageId": "rust", "version": 1, "text": WORKSPACE["lib.rs"]}})
    # didOpen parses off-thread. Wait for this buffer's first result rather
    # than accidentally testing a pre-existing disk-cache fallback.
    c.diagnostics_for(lib_uri, timeout=5)
    c.request(2, "textDocument/documentSymbol", {"textDocument": {"uri": lib_uri}})
    r = c.wait_for_id(2)
    syms = (r or {}).get("result") or []
    names = [s["name"] for s in syms]
    check("documentSymbol returns nested symbols", bool(syms))
    check("finds top-level functions", {"add", "helper", "main"} <= set(names), str(names))
    add_sym = next((s for s in syms if s["name"] == "add"), None)
    if add_sym:
        # `pub fn add(...)` — the name starts after "pub fn ".
        check("selection_range covers just the name",
              add_sym["selectionRange"]["start"] == {"line": 8, "character": 7} and
              add_sym["selectionRange"]["end"] == {"line": 8, "character": 10},
              json.dumps(add_sym["selectionRange"]))
        check("documentSymbol range includes the closing body line",
              add_sym["range"]["end"] == {"line": 10, "character": 1},
              json.dumps(add_sym["range"]))
    check("no u32::MAX ranges leak into the outline",
          all(s["range"]["end"]["character"] != 4294967295 for s in syms))

    print("\n[3] hover")
    # `add` in `pub fn add` (0-based line 8, column 8).
    c.request(3, "textDocument/hover", {"textDocument": {"uri": lib_uri},
                                        "position": {"line": 8, "character": 8}})
    r = c.wait_for_id(3)
    hv = (r or {}).get("result")
    check("hover returns markdown", bool(hv and hv.get("contents", {}).get("value")))
    if hv:
        val = hv["contents"]["value"]
        check("hover shows the docstring", "Adds two numbers" in val, val[:200])
        check("hover shows complexity", "complexity" in val, val[:200])

    print("\n[4] go to definition (same file)")
    c.request(4, "textDocument/definition", {"textDocument": {"uri": lib_uri},
                                            "position": {"line": 9, "character": 6}})
    r = c.wait_for_id(4)
    d = (r or {}).get("result")
    check("definition resolves helper() to its declaration",
          bool(d) and d[0]["targetRange"]["start"]["line"] == 12,
          json.dumps(d)[:250] if d else "none")

    print("\n[5] cross-file definition + references (needs the scan)")
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": app_uri, "languageId": "python", "version": 1, "text": WORKSPACE["app.py"]}})
    time.sleep(0.6)
    c.request(5, "textDocument/definition", {"textDocument": {"uri": app_uri},
                                            "position": {"line": 2, "character": 12}})
    r = c.wait_for_id(5, timeout=20)
    d = (r or {}).get("result")
    check("app.py's add() resolves into lib.rs",
          bool(d) and d[0]["targetUri"].endswith("lib.rs"),
          json.dumps(d)[:300] if d else "none")

    c.request(6, "textDocument/references", {
        "textDocument": {"uri": lib_uri}, "position": {"line": 8, "character": 8},
        "context": {"includeDeclaration": True}})
    r = c.wait_for_id(6, timeout=20)
    refs = (r or {}).get("result") or []
    ref_uris = {x["uri"].split("/")[-1] for x in refs}
    check("references finds call sites in lib.rs and app.py",
          "lib.rs" in ref_uris and "app.py" in ref_uris, str(ref_uris))

    print("\n[6] completion")
    c.request(7, "textDocument/completion", {
        "textDocument": {"uri": lib_uri}, "position": {"line": 9, "character": 10}})
    r = c.wait_for_id(7)
    items = (r or {}).get("result") or []
    labels = [i["label"] for i in items]
    check("completion offers helper", "helper" in labels, str(labels[:15]))

    print("\n[7] documentHighlight")
    c.request(8, "textDocument/documentHighlight", {
        "textDocument": {"uri": lib_uri}, "position": {"line": 8, "character": 8}})
    r = c.wait_for_id(8)
    hl = (r or {}).get("result") or []
    check("highlight marks declaration and call sites", len(hl) >= 2, str(len(hl)))

    print("\n[8] semantic tokens")
    c.request(9, "textDocument/semanticTokens/full", {"textDocument": {"uri": lib_uri}})
    r = c.wait_for_id(9)
    st = (r or {}).get("result")
    check("semantic tokens returned", bool(st and st.get("data")), json.dumps(st)[:200] if st else "none")
    if st and st.get("data"):
        flat = st["data"]
        check("token data is a flat u32 array of 5-tuples", len(flat) % 5 == 0, str(len(flat)))
        toks = [flat[i:i + 5] for i in range(0, len(flat), 5)]
        check("at least one semantic token emitted", len(toks) > 0)
        running = 0
        prev_line = 0
        monotonic = True
        for t in toks:
            running += t[0]
            if running < prev_line:
                monotonic = False
            prev_line = running
        check("cumulative token lines never go backwards", monotonic)
        check("every token has a positive length", all(t[2] > 0 for t in toks))

    print("\n[9] incremental edit + diagnostics")
    # Break the file: remove the closing brace of add().
    c.notify("textDocument/didChange", {
        "textDocument": {"uri": lib_uri, "version": 2},
        "contentChanges": [{
            # Drop the `{` that opens `pub fn add(...) -> i32 {`, leaving the
            # body dangling and the file genuinely unparseable.
            "range": {"start": {"line": 8, "character": 34},
                      "end": {"line": 8, "character": 35}},
            "rangeLength": 1, "text": "",
        }],
    })
    time.sleep(1.5)
    diags = c.diagnostics_for(lib_uri, latest=True)
    check("diagnostics published after an edit", diags is not None)
    if diags is not None:
        sev = [d.get("severity") for d in diags]
        check("syntax error is severity 1 (error)", 1 in sev, str(diags)[:300])

    print("\n[10] rename safety")
    c.request(10, "textDocument/prepareRename", {
        "textDocument": {"uri": lib_uri}, "position": {"line": 8, "character": 8}})
    r = c.wait_for_id(10)
    check("prepareRename offers a range for an unambiguous name",
          bool((r or {}).get("result")), json.dumps(r)[:250])

    # rename.rs declares twice() and calls it twice on one line of the same file.
    rename_uri = pathlib.Path(tmp, "rename.rs").as_uri()
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": rename_uri, "languageId": "rust", "version": 1, "text": WORKSPACE["rename.rs"]}})
    time.sleep(0.5)
    c.request(101, "textDocument/rename", {
        "textDocument": {"uri": rename_uri}, "position": {"line": 0, "character": 4},
        "newName": "double"})
    r = c.wait_for_id(101)
    edits = ((r or {}).get("result") or {}).get("changes", {}).get(rename_uri, [])
    spots = sorted((e["range"]["start"]["line"], e["range"]["start"]["character"]) for e in edits)
    check("rename edits the declaration and every call in the same file",
          spots == [(0, 3), (5, 4), (5, 14)], str(spots))

    print("\n[11] didChangeWatchedFiles + call hierarchy")
    newfile = pathlib.Path(tmp, "extra.rs")
    newfile.write_text("fn brand_new() -> i32 { 7 }\n", encoding="utf-8")
    c.notify("workspace/didChangeWatchedFiles", {"changes": [
        {"uri": newfile.as_uri(), "type": 1}]})
    time.sleep(1.0)
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": newfile.as_uri(), "languageId": "rust", "version": 1,
        "text": newfile.read_text()}})
    c.diagnostics_for(newfile.as_uri(), timeout=5)
    c.request(11, "textDocument/prepareCallHierarchy", {
        "textDocument": {"uri": newfile.as_uri()}, "position": {"line": 0, "character": 4}})
    r = c.wait_for_id(11)
    check("prepareCallHierarchy finds the new file's symbol",
          bool((r or {}).get("result")), json.dumps(r)[:250])

    print("\n[12] unparseable file must not brick the buffer")
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": broken_uri, "languageId": "rust", "version": 1, "text": WORKSPACE["broken.rs"]}})
    time.sleep(1.0)
    c.request(12, "textDocument/documentSymbol", {"textDocument": {"uri": broken_uri}})
    r = c.wait_for_id(12)
    check("a syntactically broken file still answers documentSymbol (no crash)",
          r is not None and "result" in r, json.dumps(r)[:250])
    # And an edit to it must still be accepted.
    c.notify("textDocument/didChange", {
        "textDocument": {"uri": broken_uri, "version": 2},
        "contentChanges": [{"text": "fn recovered() -> i32 { 1 }\n"}]})
    time.sleep(1.0)
    c.request(13, "textDocument/documentSymbol", {"textDocument": {"uri": broken_uri}})
    r = c.wait_for_id(13)
    syms13 = (r or {}).get("result") or []
    check("recovered symbols appear after fixing the file",
          "recovered" in [s["name"] for s in syms13], str([s["name"] for s in syms13]))

    print("\n[13] symbol and position regressions")
    ticket = 1000

    def request(method, params):
        nonlocal ticket
        ticket += 1
        c.request(ticket, method, params)
        return c.wait_for_id(ticket)

    def workspace_names(query):
        response = request("workspace/symbol", {"query": query})
        return [s["name"] for s in (response or {}).get("result") or []]

    def wait_names(query, predicate):
        deadline = time.monotonic() + 5
        names = []
        while time.monotonic() < deadline:
            names = workspace_names(query)
            if predicate(names):
                break
            time.sleep(0.02)
        return names

    def open_text(path, text, language="python"):
        c.notify("textDocument/didOpen", {"textDocument": {
            "uri": path.as_uri(), "languageId": language, "version": 1, "text": text}})

    labels = pathlib.Path(tmp, "labels148.rs")
    open_text(labels, "fn runner_148() {}", "rust")
    names = wait_names("runner_", lambda names: "runner_148" in names)
    check("workspace/symbol returns the real name for a prefix query",
          names == ["runner_148"], str(names))

    eof = pathlib.Path(tmp, "eof148.py")
    open_text(eof, "def tail148(): pass\ntail148")
    wait_names("tail148", lambda names: names == ["tail148"])
    response = request("textDocument/prepareRename", {
        "textDocument": {"uri": eof.as_uri()}, "position": {"line": 1, "character": 7}})
    eof_range = (response or {}).get("result")
    check("EOF identifier range retains its last character without newline",
          eof_range == {"start": {"line": 1, "character": 0},
                        "end": {"line": 1, "character": 7}}, str(eof_range))

    crlf = pathlib.Path(tmp, "crlf148.rs")
    open_text(crlf, "fn before148() {}\r\nfn keep148() {}", "rust")
    wait_names("before148", lambda names: names == ["before148"])
    c.notify("textDocument/didChange", {
        "textDocument": {"uri": crlf.as_uri(), "version": 2},
        "contentChanges": [{"range": {"start": {"line": 0, "character": 0},
                                     "end": {"line": 0, "character": 999}},
                            "text": "fn after148() {}"}]})
    wait_names("after148", lambda names: names == ["after148"])
    response = request("textDocument/documentSymbol", {"textDocument": {"uri": crlf.as_uri()}})
    keep = next((s for s in (response or {}).get("result") or [] if s["name"] == "keep148"), None)
    check("past-end CRLF edit preserves the following line",
          keep is not None and keep["range"]["start"]["line"] == 1, str(keep))

    tested = pathlib.Path(tmp, "test148.rs")
    open_text(tested, "#[test]\nfn sample148() {}\n", "rust")
    wait_names("sample148", lambda names: names == ["sample148"])
    response = request("textDocument/semanticTokens/full", {"textDocument": {"uri": tested.as_uri()}})
    flat = ((response or {}).get("result") or {}).get("data", [])
    functions = [flat[i:i + 5] for i in range(0, len(flat), 5) if flat[i + 3] == 0]
    deprecated = caps["semanticTokensProvider"]["legend"]["tokenModifiers"].index("deprecated")
    check("Rust test functions are not marked deprecated over JSON-RPC",
          bool(functions) and all(not (t[4] & (1 << deprecated)) for t in functions), str(functions))

    print("\n[14] buffer and watcher regressions")
    ticket = 2000

    def request(method, params):
        nonlocal ticket
        ticket += 1
        c.request(ticket, method, params)
        return c.wait_for_id(ticket)

    def workspace_names(query):
        response = request("workspace/symbol", {"query": query})
        return [s["name"] for s in (response or {}).get("result") or []]

    def wait_names(query, predicate):
        deadline = time.monotonic() + 5
        names = []
        while time.monotonic() < deadline:
            names = workspace_names(query)
            if predicate(names):
                break
            time.sleep(0.02)
        return names

    def open_text(path, text, language="python"):
        c.notify("textDocument/didOpen", {"textDocument": {
            "uri": path.as_uri(), "languageId": language, "version": 1, "text": text}})

    def change_events(changes):
        c.notify("workspace/didChangeWatchedFiles", {"changes": [
            {"uri": path.as_uri(), "type": kind} for path, kind in changes]})

    unsaved = pathlib.Path(tmp, "unsaved148.py")
    open_text(unsaved, "def never_saved_148(): pass\n")
    names = wait_names("never_saved_148", lambda names: names == ["never_saved_148"])
    check("never-saved buffer is indexed while open", names == ["never_saved_148"], str(names))
    c.notify("textDocument/didClose", {"textDocument": {"uri": unsaved.as_uri()}})
    names = wait_names("never_saved_148", lambda names: not names)
    check("closing a never-saved buffer removes its workspace symbols", not names, str(names))

    watched_path = pathlib.Path(tmp, "watched148.py")
    watched_path.write_text("def inside_watch_148(): pass\n", encoding="utf-8")
    with tempfile.TemporaryDirectory(prefix=pathlib.Path(tmp).name + "-outside-",
                                     dir=pathlib.Path(tmp).parent) as outside_dir:
        outside_path = pathlib.Path(outside_dir, "foreign.py")
        outside_path.write_text("def outside_watch_148(): pass\n", encoding="utf-8")
        # The accepted second event is a barrier for handling the first one.
        change_events([(outside_path, 1), (watched_path, 1)])
        names = wait_names("inside_watch_148", lambda names: names == ["inside_watch_148"])
        check("an in-workspace watched creation is indexed", names == ["inside_watch_148"], str(names))
        names = workspace_names("outside_watch_148")
        check("a sibling-prefix file outside the workspace is not indexed", not names, str(names))

    open_text(watched_path, "def buffer_watch_148(): pass\n")
    wait_names("buffer_watch_148", lambda names: names == ["buffer_watch_148"])
    watched_path.write_text("def disk_watch_148(): pass\n", encoding="utf-8")
    barrier = pathlib.Path(tmp, "barrier148.rs")
    barrier.write_text("fn watch_barrier_148() {}", encoding="utf-8")
    change_events([(watched_path, 2), (barrier, 1)])
    wait_names("watch_barrier_148", lambda names: names == ["watch_barrier_148"])
    names = workspace_names("buffer_watch_148")
    check("watched disk edits preserve an open buffer's index", names == ["buffer_watch_148"], str(names))
    c.notify("textDocument/didClose", {"textDocument": {"uri": watched_path.as_uri()}})
    names = wait_names("disk_watch_148", lambda names: names == ["disk_watch_148"])
    check("closing an edited saved buffer restores disk symbols", names == ["disk_watch_148"], str(names))
    watched_path.unlink()
    barrier.write_text("fn watch_barrier_done_148() {}", encoding="utf-8")
    change_events([(watched_path, 3), (barrier, 2)])
    wait_names("watch_barrier_done_148", lambda names: names == ["watch_barrier_done_148"])
    names = workspace_names("disk_watch_148")
    check("an in-workspace watched deletion removes disk symbols", not names, str(names))

    diagnostics_path = pathlib.Path(tmp, "buffer_diagnostics.py")
    diagnostics_path.write_text("def disk_diagnostics(): pass\n", encoding="utf-8")
    open_text(diagnostics_path, "def buffer_diagnostics():\n    pass # TODO fix\n")
    initial_diags = c.diagnostics_for(diagnostics_path.as_uri(), timeout=5)
    check("open buffer publishes its TODO diagnostic",
          any(d.get("code") == "todo" for d in initial_diags or []), str(initial_diags))
    with c.lock:
        before_deletion = len(c.msgs)
    diagnostics_path.unlink()
    barrier.write_text("fn deletion_diagnostics_barrier() {}", encoding="utf-8")
    change_events([(diagnostics_path, 3), (barrier, 2)])
    wait_names("deletion_diagnostics_barrier", lambda names: names == ["deletion_diagnostics_barrier"])
    with c.lock:
        after_deletion = [m["params"] for m in c.msgs[before_deletion:]
                          if m.get("method") == "textDocument/publishDiagnostics"
                          and m.get("params", {}).get("uri") == diagnostics_path.as_uri()]
    check("watched deletion keeps diagnostics owned by the open buffer",
          not any(not params.get("diagnostics") for params in after_deletion), str(after_deletion))

    print("\n[15] shutdown")
    c.request(99, "shutdown")
    r = c.wait_for_id(99, timeout=10)
    if r is None or "result" not in r:
        print("    (shutdown response:", json.dumps(r)[:400], ")")
    check("shutdown acknowledged", r is not None and "result" in r)
    c.notify("exit")
    try:
        proc.wait(timeout=10)
        check("process exits after exit notification", True)
    except subprocess.TimeoutExpired:
        proc.kill()
        check("process exits after exit notification", False, "did not exit")

    if FAIL:
        print("\n--- server stderr ---")
        print(bytes(errbuf).decode("utf8", "replace")[-3000:])

    # Re-walk the byte stream: every frame must be a Content-Length header
    # followed by a body that parses as JSON. Anything else on stdout is a
    # protocol-corrupting stray print.
    raw = bytes(c.stdout_raw)
    stray, pos, frames_ok = [], 0, 0
    while pos < len(raw):
        head_end = raw.find(b"\r\n\r\n", pos)
        if head_end == -1:
            stray.append(raw[pos:pos + 80])
            break
        header = raw[pos:head_end]
        if not header.lower().startswith(b"content-length:"):
            stray.append(header[:80])
            pos = head_end + 4
            continue
        length = int(header.split(b":")[1].strip())
        body = raw[head_end + 4:head_end + 4 + length]
        if len(body) < length:
            break
        try:
            json.loads(body)
            frames_ok += 1
        except json.JSONDecodeError:
            stray.append(body[:80])
        pos = head_end + 4 + length
    check("stdout carried only well-formed JSON-RPC frames", not stray, str(stray[:3]))
    check("a non-trivial number of frames were exchanged", frames_ok > 15, str(frames_ok))

    print(f"\n{'='*60}\n  {len(PASS)} passed, {len(FAIL)} failed")
    if FAIL:
        for f in FAIL:
            print(f"    FAILED: {f}")
    print(f"{'='*60}")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
