//! FileSync shared Rust operation kernel.
//! Rust hosts use typed APIs; Go consumes the stable fc_* C ABI; Android uses JNI.
pub mod file;
mod file_ops;
pub mod mesh;
pub mod node_selection;
mod sys_info;
pub mod upload_planner;
#[cfg(windows)]
pub mod windows_tunnel;
pub use file_ops::*;
#[cfg(feature = "upload")]
pub mod upload;
