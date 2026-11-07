//! FileSync shared Rust operation kernel.
//! Rust hosts use typed APIs; Go consumes the stable fc_* C ABI; Android uses JNI.
pub mod file_core;
pub mod net_core;
mod sys_info;

pub use file_core::file;
pub use file_core::file_ops::*;
pub use file_core::receiver;
pub use file_core::receiver_ffi::*;
#[cfg(feature = "upload")]
pub use net_core::upload;
#[cfg(windows)]
pub use net_core::windows_tunnel;
pub use net_core::{mesh, node_selection, upload_planner};
