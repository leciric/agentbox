//! Builds the agentbox command-line tool into the app (src/cli.rs), when
//! packaging says where it is:
//!
//!   AGENTBOX_BUNDLED_CLI        this platform's agentbox (agentbox.exe)
//!   AGENTBOX_BUNDLED_CLI_LINUX  on a Mac and on Windows, the Linux agentbox
//!                               their front end installs in the VM or distro
//!
//! Without them (a build from a checkout) the app has no tool of its own and
//! uses whichever is installed.
use std::path::PathBuf;

fn main() {
    println!("cargo::rustc-check-cfg=cfg(bundled_cli)");
    println!("cargo::rustc-check-cfg=cfg(bundled_cli_linux)");
    for (var, cfg) in [
        ("AGENTBOX_BUNDLED_CLI", "bundled_cli"),
        ("AGENTBOX_BUNDLED_CLI_LINUX", "bundled_cli_linux"),
    ] {
        println!("cargo::rerun-if-env-changed={var}");
        let Some(path) = std::env::var_os(var).filter(|p| !p.is_empty()) else {
            continue;
        };
        // include_bytes! reads relative to the source file: make it absolute.
        let path = PathBuf::from(path)
            .canonicalize()
            .unwrap_or_else(|e| panic!("{var}: {e}"));
        assert!(path.is_file(), "{var}: {} is not a file", path.display());
        println!("cargo::rerun-if-changed={}", path.display());
        println!("cargo::rustc-env={var}_PATH={}", path.display());
        println!("cargo::rustc-cfg={cfg}");
    }
    // The Linux one alone would be a front end with nothing to install it.
    if std::env::var_os("AGENTBOX_BUNDLED_CLI_LINUX").is_some_and(|p| !p.is_empty())
        && std::env::var_os("AGENTBOX_BUNDLED_CLI").is_none_or(|p| p.is_empty())
    {
        panic!("AGENTBOX_BUNDLED_CLI_LINUX is set without AGENTBOX_BUNDLED_CLI");
    }
}
