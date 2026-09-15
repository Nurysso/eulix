import argparse
import json
import re
import sys
from collections import Counter, defaultdict


def analyze_id_prefixes_and_patterns(json_file_path):
    try:
        with open(json_file_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except FileNotFoundError:
        print(f"Error: File '{json_file_path}' not found.", file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Error parsing JSON: {e}", file=sys.stderr)
        sys.exit(1)

    nodes = data.get("nodes", [])
    if not nodes:
        print("Warning: No 'nodes' array found in the provided JSON file.")
        return

    print(f"Analyzing {len(nodes)} nodes from '{json_file_path}'...\n")

    prefix_counts = Counter()
    pattern_counts = Counter()
    detailed_samples = defaultdict(list)

    prefix_regex = re.compile(r"^([a-zA-Z0-9]+)_[a-zA-Z0-9_-]+::")

    for node in nodes:
        node_id = str(node.get("id", ""))
        if not node_id:
            continue

        prefix_match = prefix_regex.match(node_id)
        if prefix_match:
            prefix = prefix_match.group(1)
            prefix_counts[prefix] += 1
        else:
            prefix_counts["[No standard prefix]"] += 1

        if "::" in node_id:
            left_side = node_id.split("::", 1)[0]
            if "_" in left_side:
                p_type = left_side.split("_", 1)[0]
                template = f"{p_type}_IDENT::FILE_PATH"
            else:
                template = "IDENT::FILE_PATH"
        else:
            template = "FLAT_IDENT"

        pattern_counts[template] += 1

        if len(detailed_samples[template]) < 3:
            detailed_samples[template].append(node_id)

    print("=" * 60)
    print("ID PREFIX & PATTERN ANALYSIS")
    print("=" * 60)

    print("\n[1] Detected ID Prefixes:")
    for prefix, count in prefix_counts.most_common():
        pct = (count / len(nodes)) * 100
        print(f"  - '{prefix}': {count} ({pct:.1f}%)")

    print("\n[2] Unique ID Structural Patterns:")
    for template, count in pattern_counts.most_common():
        pct = (count / len(nodes)) * 100
        print(f"\n  Pattern: `{template}` ({count} occurrences | {pct:.1f}%)")
        print("    Examples:")
        for sample in detailed_samples[template]:
            print(f"      - {sample}")


def main():
    parser = argparse.ArgumentParser(
        description="Analyze node ID prefixes and structural patterns in a call graph JSON file."
    )
    parser.add_argument(
        "json_file",
        help="Path to the JSON file to analyze (e.g., call_graph.json)",
    )

    args = parser.parse_args()
    analyze_id_prefixes_and_patterns(args.json_file)


if __name__ == "__main__":
    main()
