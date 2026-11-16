# Walkthrough scene 1: set up eulix in a fresh Flask checkout.
# Wizard steps (eulix-cli/internal/cli/setup/wizard_steps.go): extra ignore
# patterns (Enter skips), then edit eulix.toml (No), which ends the wizard
# with the default config. Scene 2 runs the analysis.
Hide
Type 'cd "$WORK" && clear'
Enter
Show

Sleep 700ms
Type "cd flask"
Sleep 300ms
Enter
Sleep 500ms
Type "eulix init"
Sleep 300ms
Enter
Wait+Screen@60s /ignore patterns/
Sleep 1.5s
Enter
Wait+Screen@30s /Edit eulix.toml/
Sleep 1.2s
Type "n"
Sleep 500ms
Enter
Wait+Screen@30s /Next steps/
Sleep 2.5s
