// commands/mod.rs
// 职责：前端 invoke() 的入口层，按域分文件。没有现成归属模块（net.rs/config.rs/
// sync_engine.rs/clipboard_sync.rs/watcher.rs/transfers.rs/ws_client.rs 各自有自己那份
// command，直接长在那些文件里，不搬来这里）的域才放在这个目录下。
pub mod credential;
pub mod file;
pub mod support;
pub mod sync_domain;
pub mod update;
pub mod user;
