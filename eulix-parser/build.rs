// build.rs
use std::process::Command;

fn main() {
    // Run `git rev-parse --short HEAD` to fetch the binary's current commit SHA
    let output = Command::new("git")
        .args(["rev-parse", "--short", "HEAD"])
        .output();

    let git_hash = match output {
        Ok(out) if out.status.success() => String::from_utf8(out.stdout)
            .unwrap_or_default()
            .trim()
            .to_string(),
        _ => "unknown".to_string(),
    };

    // Export the environment variable for compile-time access
    println!("cargo:rustc-env=VERGEN_GIT_SHA={git_hash}");

    // Re-run this build script if git HEAD changes
    println!("cargo:rerun-if-changed=.git/HEAD");
    println!("cargo:rerun-if-changed=.git/refs/");
}
