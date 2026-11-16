#!/usr/bin/env bash
# Reproduce the PRISM / parser issues in docs/Known-Issues/Parser-And-PRISM.md.
#
# Builds a small multi-language project, runs eulix_parser on it with
# --prism 2 and --prism 1, and prints every call/inheritance edge, so you can
# compare against the table in Parser-And-PRISM.md ("What the test project showed").
#
# Usage: ./repro_prism.sh [/path/to/eulix_parser] [workdir]
#   parser defaults to <repo>/eulix-parser/target/release/eulix_parser
#   workdir defaults to <repo>/script-test-dump/repro_prism
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PARSER=${1:-$ROOT/eulix-parser/target/release/eulix_parser}
WORK=${2:-$ROOT/script-test-dump/repro_prism}
mkdir -p "$WORK"
FX="$WORK/fixture"
rm -rf "$FX" "$WORK/out-v1" "$WORK/out-v2"
mkdir -p "$FX/app" "$FX/lib" "$FX/other"

cat > "$FX/app/models.py" <<'PY'
class Base:
    def validate(self):
        return True

class Form(Base):
    def save(self):
        self.validate()
        return helper()

def helper():
    return 1
PY
cat > "$FX/app/service.py" <<'PY'
from app.models import Form
def run():
    f = Form()
    f.save()
PY
cat > "$FX/lib/use.py" <<'PY'
def go(f):
    f.save()
PY
cat > "$FX/other/inh.py" <<'PY'
from app.models import Base
class Child(Base):
    def run(self):
        self.validate()
PY
cat > "$FX/svc.go" <<'GO'
package main

type Manager struct{}

func (m *Manager) Save() int { return m.load() }
func (m *Manager) load() int { return 1 }

func main() {
	m := &Manager{}
	m.Save()
	helper()
}
GO
cat > "$FX/Foo.java" <<'JAVA'
public class Foo {
    public int bar() { return baz(); }
    public int baz() { return 1; }
    public static void main(String[] a) { Foo f = new Foo(); f.bar(); }
}
JAVA
cat > "$FX/cpp1.cpp" <<'CPP'
struct S { int get() { return 1; } };
int use(S& s) { return s.get(); }
namespace ns { int f() { return 1; } }
int g() { return ns::f(); }
CPP
cat > "$FX/base.js" <<'JS'
class Base { hello() { return 1; } }
class Child extends Base { run() { return this.hello(); } }
module.exports = { Child };
JS
cat > "$FX/lib.rs" <<'RS'
pub struct P;
impl P { pub fn new() -> P { P } pub fn go(&self) -> i32 { self.inner() } fn inner(&self) -> i32 { 1 } }
pub fn top() -> i32 { let p = P::new(); p.go() }
RS
cat > "$FX/mod.ts" <<'TS'
class A { run() { return this.step(); } step() { return 1; } }
export function t() { return new A().run(); }
TS
# Cross-language case: the only `helper` besides app/models.py is Python.
cat > "$FX/xlang.py" <<'PY'
def helper():
    return 1
PY
cat > "$FX/xlang.go" <<'GO'
package main
func callsHelper() { helper() }
GO

for v in 2 1; do
  "$PARSER" -r "$FX" -o "$WORK/out-v$v/kb.json" -p "$v" -t 2 >/dev/null 2>&1
  echo "=================== PRISM v$v edges (from -> to)"
  python3 - "$WORK/out-v$v/kb_call_graph.json" <<'PY'
import json, sys
g = json.load(open(sys.argv[1]))
for f, t, ty in sorted((e["from"], e["to"], e["edge_type"]) for e in g["edges"]):
    print(f"{ty:11} {f}  ->  {t}")
print(len(g["nodes"]), "nodes,", len(g["edges"]), "edges")
PY
done
echo
echo "Fixture and outputs are in: $WORK"
