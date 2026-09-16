// config.rs
// 职责：SyncConfig 运行期状态 + config/config.yml 的加载与持久化。
// 基准目录见 app_paths.rs。token 不落 yml（避免明文凭证），仅运行期/前端 localStorage 持有。
use crate::app_paths;
use crate::device;
use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncConfig {
    pub server_url: String,
    pub ws_url: String,
    pub token: String,
    pub device_id: String,
    pub device_name: String,
    pub folder_mappings: Vec<FolderMapping>,
    pub upload_workers: usize,
    pub download_workers: usize,
    pub debounce_ms: u64,
    /// 默认同步根目录（base/sync）
    pub sync_root: String,
    /// 日志配置（对齐后端 log 段）
    #[serde(default)]
    pub log: LogConfig,
    /// 粘贴快传全局唤起快捷键（Tauri accelerator 语法），本地持久化一份，重启后不用等
    /// 一次网络往返（登录/verify）就能先用上次已知的值注册。服务端（账号级）的值以
    /// 登录/verify 响应为准，登录成功会覆盖这里。
    #[serde(default)]
    pub quick_share_hotkey: Option<String>,
    #[serde(default)]
    pub quick_share_expire_minutes: Option<i32>,
    /// 可用服务器节点列表（灾备选路用）。server_url / ws_url 永远是「当前激活节点」
    /// 的那一份拷贝——这样其它模块继续读 server_url 即可，不必知道节点的存在。
    #[serde(default = "default_nodes")]
    pub nodes: Vec<ServerNode>,
    /// 当前激活节点 id
    #[serde(default)]
    pub active_node_id: String,
    /// 自动灾备：探测到当前节点不可用时自动切到延迟最低的可用节点
    #[serde(default = "default_true")]
    pub auto_failover: bool,
    /// 健康探测间隔（分钟）。低频即可——它只是兜底确认链路还活着，
    /// 真正的故障发现主要靠请求失败时的即时探测。
    #[serde(default = "default_health_interval")]
    pub health_interval_minutes: u64,
}

/// 一个可用的服务器入口。两个内置节点对应两条 frp 隧道（同一台后端、不同中转），
/// 所以它们的 ping 里 node 名会相同，选路只看连通性与延迟。
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct ServerNode {
    /// 稳定标识，内置节点用固定字符串，自定义节点用 uuid
    pub id: String,
    /// 展示名
    pub name: String,
    pub server_url: String,
    pub ws_url: String,
    /// 内置节点不可删除（可以改地址，改完 builtin 仍为 true）
    #[serde(default)]
    pub builtin: bool,
}

impl ServerNode {
    /// 由 server_url 推导 ws_url：http→ws / https→wss，其余原样。
    pub fn derive_ws_url(server_url: &str) -> String {
        let s = server_url.trim_end_matches('/');
        if let Some(rest) = s.strip_prefix("https://") {
            format!("wss://{}", rest)
        } else if let Some(rest) = s.strip_prefix("http://") {
            format!("ws://{}", rest)
        } else {
            s.to_string()
        }
    }
}

/// 两个内置节点：ddns = 主入口，jp = 东京中转。地址对应 frpc 配置里两条隧道的域名。
pub fn default_nodes() -> Vec<ServerNode> {
    vec![
        ServerNode {
            id: "ddns".into(),
            name: "主节点 · ddns".into(),
            server_url: "https://ddns.sunyuanling.cn/file".into(),
            ws_url: "wss://ddns.sunyuanling.cn/file".into(),
            builtin: true,
        },
        ServerNode {
            id: "jp".into(),
            name: "备用节点 · 东京".into(),
            // 东京这条隧道在 VPS 上占的是 8443（frpc_akile.toml 里 local 443 → remote 8443），
            // 不是标准 443，端口不能省
            server_url: "https://jp.sunyuanling.cn:8443/file".into(),
            ws_url: "wss://jp.sunyuanling.cn:8443/file".into(),
            builtin: true,
        },
    ]
}

fn default_health_interval() -> u64 {
    15
}

/// 本地目录 ↔ 服务器目录的映射，含 server 侧 folder_id
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FolderMapping {
    pub local_path: String,
    pub remote_path: String,
    /// 服务端 SyncFolder.id，注册后回填
    pub folder_id: u64,
}

/// config.yml 持久化字段（不含 token / device_id 等运行期值）。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FileConfig {
    pub server_url: String,
    pub ws_url: String,
    pub upload_workers: usize,
    pub download_workers: usize,
    pub debounce_ms: u64,
    pub sync_root: String,
    #[serde(default)]
    pub log: LogConfig,
    #[serde(default)]
    pub quick_share_hotkey: Option<String>,
    #[serde(default)]
    pub quick_share_expire_minutes: Option<i32>,
    #[serde(default = "default_nodes")]
    pub nodes: Vec<ServerNode>,
    #[serde(default)]
    pub active_node_id: String,
    #[serde(default = "default_true")]
    pub auto_failover: bool,
    #[serde(default = "default_health_interval")]
    pub health_interval_minutes: u64,
}

/// 日志配置，字段命名与后端 config.yaml 的 log 段一致。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LogConfig {
    /// 日志级别：debug / info / warn / error
    #[serde(default = "default_level")]
    pub level: String,
    /// 是否写文件
    #[serde(default = "default_true")]
    pub file: bool,
    /// 是否输出到控制台（GUI app 默认关）
    #[serde(default)]
    pub console: bool,
    /// 输出格式：console / json
    #[serde(default = "default_format")]
    pub format: String,
    /// 单文件最大大小（MB）
    #[serde(default = "default_max_size")]
    #[serde(rename = "max_size")]
    pub max_size: u64,
    /// 最大备份文件数
    #[serde(default = "default_max_backup")]
    #[serde(rename = "max_backup")]
    pub max_backup: u32,
    /// 最大保留天数
    #[serde(default = "default_max_age")]
    #[serde(rename = "max_age")]
    pub max_age: u32,
}

impl Default for LogConfig {
    fn default() -> Self {
        LogConfig {
            level: default_level(),
            file: true,
            console: false,
            format: default_format(),
            max_size: default_max_size(),
            max_backup: default_max_backup(),
            max_age: default_max_age(),
        }
    }
}

fn default_level() -> String {
    "info".into()
}
fn default_true() -> bool {
    true
}
fn default_format() -> String {
    "console".into()
}
fn default_max_size() -> u64 {
    100
}
fn default_max_backup() -> u32 {
    3
}
fn default_max_age() -> u32 {
    7
}

impl Default for SyncConfig {
    fn default() -> Self {
        SyncConfig {
            server_url: "https://ddns.sunyuanling.cn/file".into(),
            ws_url: "wss://ddns.sunyuanling.cn/file".into(),
            token: String::new(),
            device_id: device::generate_device_id(),
            device_name: device::hostname(),
            folder_mappings: vec![],
            upload_workers: 4,
            download_workers: 4,
            debounce_ms: 800,
            sync_root: app_paths::sync_dir().to_string_lossy().to_string(),
            log: LogConfig::default(),
            quick_share_hotkey: None,
            quick_share_expire_minutes: None,
            nodes: default_nodes(),
            active_node_id: "ddns".into(),
            auto_failover: true,
            health_interval_minutes: default_health_interval(),
        }
    }
}

impl SyncConfig {
    /// 从 config.yml 加载（缺失则写入默认值），device_id/device_name 始终运行期生成。
    pub fn load() -> Self {
        let mut cfg = SyncConfig::default();
        match std::fs::read_to_string(app_paths::config_file()) {
            Ok(text) => match serde_yaml::from_str::<FileConfig>(&text) {
                Ok(fc) => cfg.apply_file(fc),
                Err(_) => cfg.save(), // 解析失败则用默认值覆盖回写
            },
            Err(_) => cfg.save(), // 首次运行：落默认 config.yml
        }
        cfg.normalize_nodes();
        cfg
    }

    /// 把当前配置的持久化子集写回 config.yml。
    pub fn save(&self) {
        let fc = FileConfig {
            server_url: self.server_url.clone(),
            ws_url: self.ws_url.clone(),
            upload_workers: self.upload_workers,
            download_workers: self.download_workers,
            debounce_ms: self.debounce_ms,
            sync_root: self.sync_root.clone(),
            log: self.log.clone(),
            quick_share_hotkey: self.quick_share_hotkey.clone(),
            quick_share_expire_minutes: self.quick_share_expire_minutes,
            nodes: self.nodes.clone(),
            active_node_id: self.active_node_id.clone(),
            auto_failover: self.auto_failover,
            health_interval_minutes: self.health_interval_minutes,
        };
        if let Ok(text) = serde_yaml::to_string(&fc) {
            let _ = std::fs::write(app_paths::config_file(), text);
        }
    }

    fn apply_file(&mut self, fc: FileConfig) {
        self.server_url = fc.server_url;
        self.ws_url = fc.ws_url;
        if fc.upload_workers > 0 {
            self.upload_workers = fc.upload_workers;
        }
        if fc.download_workers > 0 {
            self.download_workers = fc.download_workers;
        }
        if fc.debounce_ms > 0 {
            self.debounce_ms = fc.debounce_ms;
        }
        if !fc.sync_root.is_empty() {
            self.sync_root = fc.sync_root;
        }
        self.log = fc.log;
        if fc.quick_share_hotkey.is_some() {
            self.quick_share_hotkey = fc.quick_share_hotkey;
        }
        if fc.quick_share_expire_minutes.is_some() {
            self.quick_share_expire_minutes = fc.quick_share_expire_minutes;
        }
        if !fc.nodes.is_empty() {
            self.nodes = fc.nodes;
        }
        self.active_node_id = fc.active_node_id;
        self.auto_failover = fc.auto_failover;
        if fc.health_interval_minutes > 0 {
            self.health_interval_minutes = fc.health_interval_minutes;
        }
    }

    /// 让 nodes / active_node_id / server_url / ws_url 四者自洽。任何一处被改过之后都要调。
    ///
    /// 处理三种历史/异常情况：
    /// 1. 老配置只有 server_url、没有 nodes —— 若它不属于任何已知节点，收编成一个自定义节点，
    ///    这样升级上来的用户不会莫名其妙被切到内置节点上去。
    /// 2. 内置节点被整段删掉（手改 yml）—— 补回来，灾备总得有备胎。
    /// 3. active_node_id 指向不存在的节点 —— 回落到第一个节点。
    pub fn normalize_nodes(&mut self) {
        // 补齐内置节点（按 id 判断，地址允许用户改）
        for builtin in default_nodes() {
            if !self.nodes.iter().any(|n| n.id == builtin.id) {
                self.nodes.push(builtin);
            }
        }
        // 老配置迁移：当前 server_url 不在任何节点里 → 收编为自定义节点并激活
        let current = self.server_url.trim_end_matches('/').to_string();
        if !current.is_empty()
            && !self
                .nodes
                .iter()
                .any(|n| n.server_url.trim_end_matches('/') == current)
        {
            let ws = if self.ws_url.is_empty() {
                ServerNode::derive_ws_url(&current)
            } else {
                self.ws_url.clone()
            };
            let id = format!("custom-{}", uuid::Uuid::new_v4());
            self.nodes.push(ServerNode {
                id: id.clone(),
                name: "自定义节点".into(),
                server_url: current,
                ws_url: ws,
                builtin: false,
            });
            self.active_node_id = id;
        }
        // active 指向不存在的节点 → 回落第一个
        if !self.nodes.iter().any(|n| n.id == self.active_node_id) {
            self.active_node_id = self.nodes.first().map(|n| n.id.clone()).unwrap_or_default();
        }
        // 把激活节点的地址同步到 server_url / ws_url（其它模块只认这两个字段）
        if let Some(node) = self.active_node().cloned() {
            self.server_url = node.server_url;
            self.ws_url = node.ws_url;
        }
    }

    pub fn active_node(&self) -> Option<&ServerNode> {
        self.nodes.iter().find(|n| n.id == self.active_node_id)
    }

    /// 直接指定要用的服务器地址（老的「服务器设置」入口走这里）。
    /// 地址属于已知节点就激活那个节点，否则收编成一条自定义节点再激活 ——
    /// 保证任何时候 server_url 都对应节点表里的某一项，灾备逻辑才有得选。
    ///
    /// 返回激活的节点 id。
    pub fn use_server_url(&mut self, server_url: &str, ws_url: &str) -> String {
        let url = server_url.trim().trim_end_matches('/').to_string();
        let ws = if ws_url.trim().is_empty() {
            ServerNode::derive_ws_url(&url)
        } else {
            ws_url.trim().trim_end_matches('/').to_string()
        };
        let existing = self
            .nodes
            .iter()
            .find(|n| n.server_url.trim_end_matches('/') == url)
            .map(|n| n.id.clone());
        let id = match existing {
            Some(id) => id,
            None => {
                let id = format!("custom-{}", uuid::Uuid::new_v4());
                self.nodes.push(ServerNode {
                    id: id.clone(),
                    name: "自定义节点".into(),
                    server_url: url.clone(),
                    ws_url: ws.clone(),
                    builtin: false,
                });
                id
            }
        };
        self.active_node_id = id.clone();
        self.server_url = url;
        self.ws_url = ws;
        id
    }
}

pub type SharedSyncConfig = Arc<RwLock<SyncConfig>>;

pub fn init_sync_config() -> SharedSyncConfig {
    Arc::new(RwLock::new(SyncConfig::load()))
}
