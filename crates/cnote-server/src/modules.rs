use cnote_core::{Module, Registry};
use std::sync::Arc;

/// Every module's commands, hooks, event handlers, jobs and sweeps. The only place modules are wired together.
pub fn registry() -> Arc<Registry> {
    Registry::new(all_modules())
}

pub fn all_modules() -> Vec<Module> {
    vec![
        cnote_core::module(),
        cnote_docs::module(),
        cnote_finance::module(),
        // MODULES: each crate exposes `pub fn module() -> cnote_core::Module`
    ]
}
