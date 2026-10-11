use crate::struc::kb_struct::KnowledgeBase;

use super::{call_graph, dependencies, entry_points, patterns};

pub fn analyze_and_build(mut kb: KnowledgeBase, verbose: bool, prism: u8) -> KnowledgeBase {
    let file_count = kb.structure.len();
    let is_large = file_count > 100_000;
    let use_precise = prism;

    if verbose && is_large {
        println!(
            "   [!]  Enabling memory-efficient mode for {} files",
            file_count
        );
    }

    if !is_large {
        if use_precise != 2 {
            if verbose {
                println!("   → Resolving call locations...");
            }
            call_graph::prism_v1::resolve_call_locations(&mut kb);
        }

        if verbose {
            println!("   → Building call graphs...");
        }
        kb.call_graph = if use_precise == 2 {
            if verbose {
                println!("      Using precise analysis (PRISMv2)...");
            }
            call_graph::prism_v2::build_call_graph_v2(&kb.structure)
        } else {
            if verbose {
                println!("      Using direct analysis (PRISMv1)...");
            }
            call_graph::prism_v1::build_call_graph(&kb.structure)
        };

        if verbose {
            println!("   → Building reverse call graphs...");
        }
        if use_precise == 2 {
            call_graph::prism_v2::populate_called_by_from_graph(&mut kb);
        } else {
            call_graph::prism_v1::populate_called_by(&mut kb);
        }
    } else if verbose {
        println!("   [!]  Skipping call graph (too large, would use excessive memory)");
    }

    if verbose {
        println!("   → Detecting patterns...");
    }
    kb.patterns = patterns::detect_patterns(&kb);

    if verbose {
        println!("   → Finding entry points...");
    }
    kb.entry_points = entry_points::find_entry_points(&kb);

    if verbose {
        println!("   → Analyzing dependencies...");
    }
    kb.external_dependencies = dependencies::analyze_external_deps(&kb);

    kb
}
