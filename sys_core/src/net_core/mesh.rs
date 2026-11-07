//! Shared mesh route decisions. Hosts supply tunnel observations; HTTP addresses never become UDP candidates.
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Peer {
    pub pubkey: String,
    pub allowed_ips: Vec<String>,
    #[serde(default)]
    pub endpoint: String,
    #[serde(default)]
    pub candidates: Vec<String>,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub confirmed_endpoint: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Input {
    pub peers: Vec<Peer>,
    #[serde(default)]
    pub local_networks: Vec<String>,
    /// Hosts advance every 15 seconds. Each candidate gets 30 seconds;
    /// a relay carries traffic throughout unconfirmed direct probes.
    pub round: u64,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Route {
    pub pubkey: String,
    pub allowed_ips: Vec<String>,
    pub endpoint: String,
    pub mode: String,
}

pub fn plan(input: &Input) -> Vec<Route> {
    let server = input.peers.iter().find(|p| {
        p.role == "server" && !p.endpoint.is_empty() && !p.endpoint.chars().any(char::is_whitespace)
    });
    let mut routes = Vec::new();
    let mut relay_ips = Vec::new();
    for peer in input.peers.iter().filter(|p| p.role != "server") {
        let mut candidates = peer.candidates.clone();
        if !peer.endpoint.is_empty() && !candidates.contains(&peer.endpoint) {
            candidates.push(peer.endpoint.clone());
        }
        candidates.retain(|ep| {
            ep.parse::<std::net::SocketAddr>().map_or(false, |ap| {
                ap.port() != 0 && !ap.ip().is_loopback() && !ap.ip().is_unspecified()
            })
        });
        if !input.local_networks.is_empty() {
            candidates.sort_by_key(|ep| rank_endpoint(ep, &input.local_networks));
        }
        let confirmed = peer
            .confirmed_endpoint
            .as_ref()
            .filter(|ep| candidates.contains(ep))
            .cloned();
        let endpoint = confirmed.clone().or_else(|| {
            if candidates.is_empty() {
                None
            } else {
                candidates
                    .get((input.round / 2 % candidates.len() as u64) as usize)
                    .cloned()
            }
        });
        if let Some(endpoint) = endpoint {
            // Keep a working relay route while WireGuard probes the direct endpoint
            // using its keepalive/handshake. An unconfirmed peer must not steal the
            // destination prefix: symmetric NAT may reject every direct attempt.
            let probing_via_relay = confirmed.is_none() && server.is_some();
            if probing_via_relay {
                relay_ips.extend(peer.allowed_ips.iter().cloned());
            }
            routes.push(Route {
                pubkey: peer.pubkey.clone(),
                allowed_ips: if probing_via_relay {
                    Vec::new()
                } else {
                    peer.allowed_ips.clone()
                },
                endpoint,
                mode: if confirmed.is_some() {
                    "direct"
                } else {
                    "probing"
                }
                .into(),
            });
        } else if server.is_some() {
            relay_ips.extend(peer.allowed_ips.iter().cloned());
        }
    }
    if let Some(server) = server {
        let mut endpoints = server.candidates.clone();
        if !endpoints.contains(&server.endpoint) {
            endpoints.push(server.endpoint.clone());
        }
        endpoints.retain(|ep| !ep.is_empty() && !ep.chars().any(char::is_whitespace));
        if !input.local_networks.is_empty() {
            endpoints.sort_by_key(|ep| rank_endpoint(ep, &input.local_networks));
        }
        let endpoint = server
            .confirmed_endpoint
            .as_ref()
            .filter(|ep| endpoints.contains(ep))
            .cloned()
            .unwrap_or_else(|| {
                endpoints[(input.round / 2 % endpoints.len() as u64) as usize].clone()
            });
        relay_ips.extend(server.allowed_ips.iter().cloned());
        relay_ips.sort();
        relay_ips.dedup();
        routes.push(Route {
            pubkey: server.pubkey.clone(),
            allowed_ips: relay_ips,
            endpoint,
            mode: "relay".into(),
        });
    }
    routes
}

fn rank_endpoint(endpoint: &str, networks: &[String]) -> u8 {
    let Ok(address) = endpoint.parse::<std::net::SocketAddr>() else {
        return 1;
    };
    let std::net::IpAddr::V4(ip) = address.ip() else {
        return 1;
    };
    for network in networks {
        let Some((base, bits)) = network.split_once('/') else {
            continue;
        };
        let (Ok(base), Ok(bits)) = (base.parse::<std::net::Ipv4Addr>(), bits.parse::<u32>()) else {
            continue;
        };
        if !(1..=32).contains(&bits) {
            continue;
        }
        let mask = u32::MAX << (32 - bits);
        if u32::from(base) & mask == u32::from(ip) & mask {
            return 0;
        }
    }
    if ip.is_private() {
        2
    } else {
        1
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn data() -> Input {
        Input {
            round: 0,
            local_networks: vec![],
            peers: vec![
                Peer {
                    pubkey: "peer".into(),
                    allowed_ips: vec!["172.1.0.1/32".into()],
                    endpoint: "203.0.113.1:1234".into(),
                    candidates: vec!["192.168.31.2:51821".into()],
                    role: "member".into(),
                    confirmed_endpoint: None,
                },
                Peer {
                    pubkey: "server".into(),
                    allowed_ips: vec!["172.1.0.254/32".into()],
                    endpoint: "relay.example:12000".into(),
                    candidates: vec![],
                    role: "server".into(),
                    confirmed_endpoint: None,
                },
            ],
        }
    }
    #[test]
    fn lan_first_then_public_then_relay_without_route_blackhole() {
        let mut d = data();
        assert_eq!(plan(&d)[0].endpoint, "192.168.31.2:51821");
        d.round = 2;
        assert_eq!(plan(&d)[0].endpoint, "203.0.113.1:1234");
        d.round = 4;
        let r = plan(&d);
        assert_eq!(r.len(), 2);
        assert!(r[0].allowed_ips.is_empty());
        assert!(r[1].allowed_ips.contains(&"172.1.0.1/32".into()));
    }
    #[test]
    fn successful_direct_path_survives_relay_window() {
        let mut d = data();
        d.round = 4;
        d.peers[0].confirmed_endpoint = Some("192.168.31.2:51821".into());
        let r = plan(&d);
        assert_eq!(r[0].mode, "direct");
        assert!(!r[1].allowed_ips.contains(&"172.1.0.1/32".into()));
    }
    #[test]
    fn unconfirmed_probes_never_take_prefix_from_relay_at_any_phase() {
        let mut d = data();
        for round in 0..18 {
            d.round = round;
            let routes = plan(&d);
            let owner = routes
                .iter()
                .filter(|p| p.allowed_ips.contains(&"172.1.0.1/32".into()))
                .collect::<Vec<_>>();
            assert_eq!(owner.len(), 1, "exactly one route owner at round {round}");
            assert_eq!(owner[0].pubkey, "server");
            for p in routes.iter().filter(|p| p.pubkey == "peer") {
                assert!(p.allowed_ips.is_empty());
                assert_eq!(p.mode, "probing");
            }
        }
    }
    #[test]
    fn without_relay_unconfirmed_peer_still_gets_its_route() {
        let mut d = data();
        d.peers.retain(|p| p.role != "server");
        assert_eq!(plan(&d)[0].allowed_ips, vec!["172.1.0.1/32"]);
    }
    #[test]
    fn loopback_from_reverse_proxy_is_never_a_direct_candidate() {
        let mut d = data();
        d.peers[0].candidates.clear();
        d.peers[0].endpoint = "127.0.0.1:3456".into();
        assert_eq!(plan(&d).len(), 1);
    }

    fn member_peer(endpoint: &str, candidates: &[&str]) -> Peer {
        Peer {
            pubkey: "peer".into(),
            allowed_ips: vec!["172.1.0.1/32".into()],
            endpoint: endpoint.into(),
            candidates: candidates.iter().map(|s| s.to_string()).collect(),
            role: "member".into(),
            confirmed_endpoint: None,
        }
    }
    fn server_peer(endpoint: &str, candidates: &[&str]) -> Peer {
        Peer {
            pubkey: "server".into(),
            allowed_ips: vec!["172.1.0.254/32".into()],
            endpoint: endpoint.into(),
            candidates: candidates.iter().map(|s| s.to_string()).collect(),
            role: "server".into(),
            confirmed_endpoint: None,
        }
    }
    fn build_input(round: u64, local_networks: &[&str], peers: Vec<Peer>) -> Input {
        Input {
            peers,
            local_networks: local_networks.iter().map(|s| s.to_string()).collect(),
            round,
        }
    }

    #[test]
    fn local_lan_candidate_ranks_first_and_is_selected_at_round_zero() {
        let input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![member_peer(
                "10.20.30.41:51821",
                &[
                    "10.20.30.40:51821",
                    "203.0.113.9:51821",
                    "192.168.31.2:51821",
                ],
            )],
        );
        let routes = plan(&input);
        assert_eq!(routes.len(), 1);
        // round=0 选中实际 LAN，而不是输入顺序更靠前的 10.x 虚拟网卡。
        assert_eq!(routes[0].endpoint, "192.168.31.2:51821");
        assert_eq!(routes[0].mode, "probing");
    }

    #[test]
    fn public_candidate_ranks_before_unlisted_private_networks() {
        let mut input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![member_peer("", &["10.20.30.40:51821", "203.0.113.9:51821"])],
        );
        // public（rank 1）先于未列入 local_networks 的其他 private（rank 2）。
        assert_eq!(plan(&input)[0].endpoint, "203.0.113.9:51821");
        input.round = 2;
        assert_eq!(plan(&input)[0].endpoint, "10.20.30.40:51821");
    }

    #[test]
    fn server_candidates_use_same_lan_public_dns_private_rank_order() {
        // 只有 server peer：relay endpoint 的轮换不会落到中继窗口，便于逐轮断言排序。
        let mut input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![server_peer(
                "192.168.31.100:51821",
                &[
                    "10.9.8.7:51821",
                    "203.0.113.50:51821",
                    "relay.example:12000",
                ],
            )],
        );
        let routes = plan(&input);
        assert_eq!(routes.len(), 1);
        // server/relay endpoint 也按同一 rank 排序：LAN -> public -> DNS -> private。
        assert_eq!(routes[0].endpoint, "192.168.31.100:51821");
        assert_eq!(routes[0].mode, "relay");
        input.round = 2;
        assert_eq!(plan(&input)[0].endpoint, "203.0.113.50:51821");
        input.round = 4;
        assert_eq!(plan(&input)[0].endpoint, "relay.example:12000");
        input.round = 6;
        assert_eq!(plan(&input)[0].endpoint, "10.9.8.7:51821");
    }

    #[test]
    fn confirmed_endpoint_inside_candidates_is_kept_even_when_rank_is_low() {
        let mut input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![member_peer(
                "",
                &["192.168.31.2:51821", "10.20.30.40:51821"],
            )],
        );
        input.peers[0].confirmed_endpoint = Some("10.20.30.40:51821".into());
        let routes = plan(&input);
        // confirmed 地址 rank 较低也保留，且标记 direct。
        assert_eq!(routes[0].endpoint, "10.20.30.40:51821");
        assert_eq!(routes[0].mode, "direct");
    }

    #[test]
    fn confirmed_endpoint_outside_candidates_is_ignored_and_rotates() {
        let mut input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![member_peer(
                "",
                &["10.20.30.40:51821", "192.168.31.2:51821"],
            )],
        );
        input.peers[0].confirmed_endpoint = Some("203.0.113.99:9999".into());
        let routes = plan(&input);
        // 候选外 confirmed 被忽略，回退轮换：round=0 选 rank 0。
        assert_eq!(routes[0].endpoint, "192.168.31.2:51821");
        assert_eq!(routes[0].mode, "probing");
        input.round = 2;
        assert_eq!(plan(&input)[0].endpoint, "10.20.30.40:51821");
    }

    #[test]
    fn invalid_local_network_entries_are_ignored_without_panic() {
        let networks: Vec<String> = vec![
            "192.168.31.0".into(),    // 缺少 /bits
            "not-a-cidr/24".into(),   // base 不是 IPv4
            "2001:db8::/32".into(),   // IPv6 base
            "/24".into(),             // 空 base
            "192.168.31.0/0".into(),  // bits=0 非法
            "192.168.31.0/33".into(), // bits=33 非法
            "192.168.31.0/-1".into(), // bits 非 u32
            "192.168.31.0/x".into(),  // bits 非数字
        ];
        // 全部非法：跳过匹配，按 public=1、private=2 降级。
        assert_eq!(rank_endpoint("192.168.31.2:51821", &networks), 2);
        assert_eq!(rank_endpoint("10.20.30.40:51821", &networks), 2);
        assert_eq!(rank_endpoint("203.0.113.9:51821", &networks), 1);
        assert_eq!(rank_endpoint("relay.example:12000", &networks), 1);
        assert_eq!(rank_endpoint("[2001:db8::1]:51821", &networks), 1);
        let mut input = build_input(
            0,
            &[],
            vec![member_peer("", &["192.168.31.2:51821", "203.0.113.1:1234"])],
        );
        input.local_networks = networks;
        // 排序仍发生但不 panic；public 降级排在 private 前。
        assert_eq!(plan(&input)[0].endpoint, "203.0.113.1:1234");
    }

    #[test]
    fn ipv6_loopback_unspecified_and_zero_port_candidates_are_filtered() {
        let mut input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![
                member_peer(
                    "",
                    &[
                        "[::1]:51821",
                        "[::]:51821",
                        "0.0.0.0:51821",
                        "127.0.0.1:51821",
                        "192.168.31.2:0",
                        "garbage",
                        "[2001:db8::1]:51821",
                        "192.168.31.2:51821",
                    ],
                ),
                server_peer("relay.example:12000", &[]),
            ],
        );
        let routes = plan(&input);
        assert_eq!(routes.len(), 2);
        // loopback/unspecified/端口 0/无法解析的候选被剔除，round=0 选 LAN。
        assert_eq!(routes[0].endpoint, "192.168.31.2:51821");
        input.round = 2;
        let routes = plan(&input);
        // 非 loopback 的合法 IPv6 仍保留（rank 1），不 panic。
        assert_eq!(routes[0].endpoint, "[2001:db8::1]:51821");
        assert_eq!(routes[0].mode, "probing");
        input.round = 4;
        // 中继持续承载流量，探测 peer 不因窗口切换被删除/重建。
        assert_eq!(plan(&input).len(), 2);
        assert!(plan(&input)[0].allowed_ips.is_empty());
    }

    #[test]
    fn peer_without_valid_candidates_falls_back_to_relay() {
        let input = build_input(
            0,
            &["192.168.31.0/24"],
            vec![
                member_peer("", &["127.0.0.1:51821", "0.0.0.0:0", "[::1]:0", "garbage"]),
                server_peer("relay.example:12000", &[]),
            ],
        );
        let routes = plan(&input);
        assert_eq!(routes.len(), 1);
        assert_eq!(routes[0].mode, "relay");
        // 直连不可用时 member 的 AllowedIPs 交给服务器，避免路由黑洞。
        assert!(routes[0].allowed_ips.contains(&"172.1.0.1/32".into()));
        assert!(routes[0].allowed_ips.contains(&"172.1.0.254/32".into()));
    }

    #[test]
    fn legacy_order_is_unchanged_without_local_networks() {
        let mut input = build_input(
            0,
            &[],
            vec![member_peer(
                "",
                &["10.20.30.40:51821", "192.168.31.2:51821"],
            )],
        );
        // 无 local_networks：保持候选输入顺序（旧行为不回归）。
        assert_eq!(plan(&input)[0].endpoint, "10.20.30.40:51821");
        input.round = 2;
        assert_eq!(plan(&input)[0].endpoint, "192.168.31.2:51821");
        // 提供 local_networks 后才按 LAN 优先排序。
        input.local_networks = vec!["192.168.31.0/24".into()];
        input.round = 0;
        assert_eq!(plan(&input)[0].endpoint, "192.168.31.2:51821");
    }
}
