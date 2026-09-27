// app_shell/mod.rs
// 职责：纯 UI/生命周期脚手架（菜单栏、托盘、主窗口销毁重建、日志窗口、粘贴快传悬浮窗）。
// 和 commands/ 的区别：这里大部分不是 #[tauri::command]，是 run() 内部自己调的辅助函数；
// 少数几个 command（因为和某个窗口/静态状态强绑定）留在对应文件里，不搬去 commands/。
pub mod log_window;
pub mod menu;
pub mod quick_paste;
pub mod tray;
pub mod window;
