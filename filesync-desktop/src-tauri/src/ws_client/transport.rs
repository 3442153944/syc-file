// ws_client/transport.rs
// 职责：建立 WebSocket 连接。
//
// 为什么不直接用 tokio_tungstenite::connect_async：它是裸 TcpStream::connect，
//   ① 不读系统代理——只放行代理出网的公司网络里，业务 HTTP（reqwest）通、WS 却连不上；
//   ② 域名解析出多个地址（A/AAAA）时只按顺序串行尝试，没有 Happy Eyeballs，
//      优先尝试的地址是黑洞就卡住。
// 这里改成让 reqwest 发 HTTP/1.1 Upgrade 请求：代理（环境变量/系统设置/CONNECT 隧道）、
// DNS、Happy Eyeballs、TLS 全部和业务 HTTP 走同一套栈；握手成功后拿到底层连接，
// 再交给 tungstenite 做 WebSocket 帧读写。
use reqwest::header::{
    CONNECTION, SEC_WEBSOCKET_ACCEPT, SEC_WEBSOCKET_KEY, SEC_WEBSOCKET_VERSION, UPGRADE,
};
use reqwest::{ClientBuilder, StatusCode, Upgraded};
use std::fmt;
use std::time::Duration;
use tokio_tungstenite::tungstenite::handshake::client::generate_key;
use tokio_tungstenite::tungstenite::handshake::derive_accept_key;
use tokio_tungstenite::tungstenite::protocol::Role;
use tokio_tungstenite::WebSocketStream;

pub type WsStream = WebSocketStream<Upgraded>;

/// TCP/TLS 层的建连超时（整个握手另有外层超时兜底）。
const TCP_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);

/// 连接失败的分类。
#[derive(Debug)]
pub enum ConnectError {
    /// 没连上：DNS/TCP/TLS/代理隧道失败或超时。值得怀疑节点/网络，交给灾备探测。
    Transport(String),
    /// 连上了但服务端（或中间设备）没让升级：401 token 失效、代理不支持 Upgrade 等。
    /// 链路本身是通的，换节点解决不了，不该触发灾备。
    Rejected(String),
}

impl fmt::Display for ConnectError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ConnectError::Transport(m) | ConnectError::Rejected(m) => f.write_str(m),
        }
    }
}

pub async fn connect(ws_url: &str) -> Result<WsStream, ConnectError> {
    connect_with(ws_url, reqwest::Client::builder()).await
}

/// 允许调用方定制 ClientBuilder（单测里要 `.no_proxy()`，避免被本机系统代理劫走回环地址）。
pub async fn connect_with(ws_url: &str, builder: ClientBuilder) -> Result<WsStream, ConnectError> {
    // reqwest 只认 http(s)：wss→https、ws→http
    let http_url = if let Some(rest) = ws_url.strip_prefix("wss://") {
        format!("https://{}", rest)
    } else if let Some(rest) = ws_url.strip_prefix("ws://") {
        format!("http://{}", rest)
    } else {
        return Err(ConnectError::Rejected("不是 ws:// 或 wss:// 地址".into()));
    };

    let client = builder
        // Upgrade 只存在于 HTTP/1.1；不限制的话 TLS ALPN 可能协商出 h2，升级必然失败
        .http1_only()
        .connect_timeout(TCP_CONNECT_TIMEOUT)
        // 有些企业网关/WAF 直接拒绝没有 UA 的请求
        .user_agent(concat!("filesync-desktop/", env!("CARGO_PKG_VERSION")))
        .build()
        .map_err(|e| ConnectError::Transport(describe(e)))?;

    let key = generate_key();
    let resp = client
        .get(&http_url)
        .header(CONNECTION, "Upgrade")
        .header(UPGRADE, "websocket")
        .header(SEC_WEBSOCKET_VERSION, "13")
        .header(SEC_WEBSOCKET_KEY, &key)
        .send()
        .await
        .map_err(|e| ConnectError::Transport(describe(e)))?;

    if resp.status() != StatusCode::SWITCHING_PROTOCOLS {
        // 带上响应体开头一小段：服务端的 JSON 信封（token 失效走 HTTP 200 + code）和企业网关的
        // 拦截页长得都一样是「非 101」，光看状态码分不清是谁拒绝的。
        let status = resp.status();
        let snippet = body_snippet(resp).await;
        return Err(ConnectError::Rejected(if snippet.is_empty() {
            format!("HTTP {}", status)
        } else {
            format!("HTTP {}: {}", status, snippet)
        }));
    }
    // 校验服务端确实是在回应我们这次握手：中间设备改写/缓存响应时 accept 对不上
    let expect = derive_accept_key(key.as_bytes());
    match resp
        .headers()
        .get(SEC_WEBSOCKET_ACCEPT)
        .and_then(|v| v.to_str().ok())
    {
        Some(got) if got == expect => {}
        _ => {
            return Err(ConnectError::Rejected(
                "握手校验失败（Sec-WebSocket-Accept 不匹配，中间设备可能改写了响应）".into(),
            ))
        }
    }

    let upgraded = resp
        .upgrade()
        .await
        .map_err(|e| ConnectError::Transport(describe(e)))?;
    Ok(WebSocketStream::from_raw_socket(upgraded, Role::Client, None).await)
}

/// 读响应体开头最多 200 字节，压成一行。读不到/超时就当没有：这只是给人看的诊断信息，不能因此卡住重连。
async fn body_snippet(mut resp: reqwest::Response) -> String {
    const MAX: usize = 200;
    let mut buf: Vec<u8> = Vec::new();
    let read = async {
        while buf.len() < MAX {
            match resp.chunk().await {
                Ok(Some(c)) => buf.extend_from_slice(&c),
                _ => break,
            }
        }
    };
    let _ = tokio::time::timeout(Duration::from_secs(3), read).await;
    buf.truncate(MAX);
    String::from_utf8_lossy(&buf)
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

/// 把 reqwest 错误展开成人能读的一句话：去掉 URL（query 里带着 token，不能进日志/UI），
/// 补上底层原因链（"error sending request" 本身没有信息量，真正原因在 source 里）。
fn describe(e: reqwest::Error) -> String {
    let e = e.without_url();
    let mut out = e.to_string();
    let mut src = std::error::Error::source(&e);
    while let Some(s) = src {
        let t = s.to_string();
        if !out.contains(&t) {
            out.push_str(": ");
            out.push_str(&t);
        }
        src = s.source();
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use futures_util::{SinkExt, StreamExt};
    use tokio::net::TcpListener;
    use tokio_tungstenite::tungstenite::handshake::server::{ErrorResponse, Request, Response};
    use tokio_tungstenite::tungstenite::Message;

    async fn bind() -> (TcpListener, String) {
        let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!(
            "ws://127.0.0.1:{}/v1/ws/connect?token=SECRET",
            l.local_addr().unwrap().port()
        );
        (l, url)
    }

    fn no_proxy() -> ClientBuilder {
        reqwest::Client::builder().no_proxy()
    }

    #[tokio::test]
    async fn upgrades_and_echoes() {
        let (l, url) = bind().await;
        tokio::spawn(async move {
            let (s, _) = l.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(s).await.unwrap();
            while let Some(Ok(m)) = ws.next().await {
                if m.is_text() {
                    ws.send(m).await.unwrap();
                }
            }
        });
        let mut ws = connect_with(&url, no_proxy()).await.unwrap();
        ws.send(Message::Text("hi".into())).await.unwrap();
        assert_eq!(ws.next().await.unwrap().unwrap(), Message::Text("hi".into()));
    }

    #[tokio::test]
    async fn http_rejection_is_not_a_transport_error() {
        let (l, url) = bind().await;
        tokio::spawn(async move {
            let (s, _) = l.accept().await.unwrap();
            let deny = |_: &Request, _: Response| -> Result<Response, ErrorResponse> {
                let mut r = ErrorResponse::new(Some("unauthorized".to_string()));
                *r.status_mut() = tokio_tungstenite::tungstenite::http::StatusCode::UNAUTHORIZED;
                Err(r)
            };
            let _ = tokio_tungstenite::accept_hdr_async(s, deny).await;
        });
        let err = connect_with(&url, no_proxy()).await.err().unwrap();
        // 状态码和响应体开头都要在：网关拦截页靠后者才能和"token 失效"分清
        assert!(
            matches!(&err, ConnectError::Rejected(m) if m.contains("401") && m.contains("unauthorized")),
            "{:?}",
            err
        );
    }

    #[tokio::test]
    async fn refused_connection_is_a_transport_error_without_token_in_message() {
        let (l, url) = bind().await;
        drop(l); // 端口空着：连接被拒
        let err = connect_with(&url, no_proxy()).await.err().unwrap();
        assert!(matches!(err, ConnectError::Transport(_)), "{:?}", err);
        assert!(!err.to_string().contains("SECRET"), "错误信息泄露了 token: {}", err);
    }

    #[tokio::test]
    async fn non_ws_scheme_is_rejected() {
        let err = connect_with("http://127.0.0.1:1/x", no_proxy())
            .await
            .err()
            .unwrap();
        assert!(matches!(err, ConnectError::Rejected(_)));
    }
}
