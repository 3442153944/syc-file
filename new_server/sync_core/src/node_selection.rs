//! 通用入口选择决策：平台负责探测和连接，核心保持当前健康入口，故障时选最低延迟备份。
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeHealth {
    pub id: String,
    pub reachable: bool,
    pub latency_ms: u64,
    #[serde(default)]
    pub backend: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct SelectionInput {
    pub active: String,
    pub auto_failover: bool,
    /// 非空时只允许同后端入口接管，上传会话不能跨后端迁移。
    #[serde(default)]
    pub required_backend: Option<String>,
    pub nodes: Vec<NodeHealth>,
}

pub fn select(input: &SelectionInput) -> Option<&str> {
    let allowed = |node: &&NodeHealth| {
        node.reachable && input.required_backend.as_ref().map_or(true, |expected| {
            !expected.is_empty() && node.backend == *expected
        })
    };
    if let Some(active) = input.nodes.iter().filter(allowed).find(|n| n.id == input.active) {
        return Some(&active.id);
    }
    if !input.auto_failover { return None; }
    input.nodes.iter().filter(allowed).min_by_key(|n| n.latency_ms).map(|n| n.id.as_str())
}

#[cfg(test)]
mod tests {
    use super::*;
    fn input() -> SelectionInput {
        SelectionInput { active: "main".into(), auto_failover: true, required_backend: None,
            nodes: vec![NodeHealth { id: "main".into(), reachable: true, latency_ms: 100, backend: "a".into() },
                NodeHealth { id: "fast".into(), reachable: true, latency_ms: 1, backend: "b".into() },
                NodeHealth { id: "backup".into(), reachable: true, latency_ms: 20, backend: "a".into() }] }
    }
    #[test]
    fn healthy_current_is_stable() { assert_eq!(select(&input()), Some("main")); }
    #[test]
    fn failure_chooses_fastest() {
        let mut data = input(); data.nodes[0].reachable = false;
        assert_eq!(select(&data), Some("fast"));
        data.required_backend = Some("a".into()); assert_eq!(select(&data), Some("backup"));
    }
    #[test]
    fn disabled_unknown_and_all_offline_do_not_switch() {
        let mut data = input(); data.nodes[0].reachable = false; data.auto_failover = false;
        assert_eq!(select(&data), None);
        data.auto_failover = true; data.required_backend = Some(String::new()); assert_eq!(select(&data), None);
        data.required_backend = None; data.nodes.iter_mut().for_each(|n| n.reachable = false);
        assert_eq!(select(&data), None);
    }
}
