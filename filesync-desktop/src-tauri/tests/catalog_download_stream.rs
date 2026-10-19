// 使用真实 HTTP 流验证磁盘行为，不启动 Tauri 窗口或访问用户配置。
#[path = "../src/catalog_download.rs"]
mod catalog_download;

// 此处只隔离界面事件上报，实际下载和文件处理全部复用生产代码。
mod transfers {
    pub fn download_progress(_: &str, _: &str, _: Option<u64>, _: Option<String>) {}
}
