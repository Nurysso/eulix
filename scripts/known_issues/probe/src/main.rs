// Print what the parser records for each call (callee, receiver, defined_in) and
// each class's bases. Used to show: C++ doubled calls (PRISM-7), Go/Java receivers
// stored in `defined_in` (PRISM-5), JS classes with no bases (PRISM-6),
// and `.tsx`/`.jsx` being rejected (PARSE-2).
//
// Usage: cargo run -q -- file1 file2 ...
use libeulix::struc::kb_struct::Function;
use std::path::Path;

fn main() {
    for f in std::env::args().skip(1) {
        let src = std::fs::read_to_string(&f).unwrap_or_else(|e| panic!("{f}: {e}"));
        println!("== {f}");
        match libeulix::grammar_files::parse_source(Path::new(&f), &src) {
            Err(e) => println!("  PARSE ERROR: {e}"),
            Ok((_, fd)) => {
                let mut all: Vec<&Function> = fd.functions.iter().collect();
                for c in &fd.classes {
                    println!("  class {} bases={:?}", c.name, c.bases);
                    all.extend(c.methods.iter());
                }
                for func in all {
                    for c in &func.calls {
                        println!(
                            "  {} -> callee={:?} receiver={:?} defined_in={:?}",
                            func.name, c.callee, c.receiver, c.defined_in
                        );
                    }
                }
            }
        }
    }
}
