use cnote_core::{Module, Registry};
use std::sync::Arc;

/// Every module's commands, hooks, event handlers, jobs and sweeps. The only place modules are wired together.
pub fn registry() -> Arc<Registry> {
    Registry::with_ai(all_modules(), cnote_ai::provider_from_env())
}

pub fn all_modules() -> Vec<Module> {
    vec![
        cnote_core::module(),
        cnote_ai::module(),
        // MODULES: each crate exposes `pub fn module() -> cnote_core::Module`
    ]
}
