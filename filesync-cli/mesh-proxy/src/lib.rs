//! 通过 HTTP/SOCKS5 混合端口接入现有组网路由，不修改系统代理。
use base64::{engine::general_purpose::STANDARD, Engine};
use serde::{Deserialize, Serialize};
use std::{
    io,
    net::{IpAddr, Ipv4Addr, SocketAddr},
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc,
    },
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpSocket, TcpStream},
    sync::oneshot,
    task::JoinSet,
    time::timeout,
};

#[derive(Clone, Copy)]
pub struct MeshRoute {
    pub source: Ipv4Addr,
}

impl MeshRoute {
    fn allows(self, ip: IpAddr) -> bool {
        in_mesh(ip) || cfg!(test) && self.source.is_loopback() && ip == IpAddr::V4(self.source)
    }
}

/// 读取指定组网接口的地址，不读取用户会话或私钥。
pub fn interface_address(iface: &str) -> Result<Ipv4Addr, String> {
    if iface.is_empty()
        || !iface
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_-".contains(&b))
    {
        return Err("无效的隧道接口名称".into());
    }
    #[cfg(windows)]
    let out = std::process::Command::new("netsh")
        .args([
            "interface",
            "ipv4",
            "show",
            "addresses",
            &format!("name={iface}"),
        ])
        .output();
    #[cfg(not(windows))]
    let out = std::process::Command::new("ip")
        .args(["-4", "-o", "addr", "show", "dev", iface])
        .output();
    let out = out.map_err(|e| format!("读取隧道地址失败: {e}"))?;
    if !out.status.success() {
        return Err(format!("请先启动 {iface} 组网隧道，再开启端口代理"));
    }
    String::from_utf8_lossy(&out.stdout)
        .split_whitespace()
        .filter_map(|word| word.split('/').next()?.parse::<Ipv4Addr>().ok())
        .find(|ip| in_mesh(IpAddr::V4(*ip)))
        .ok_or_else(|| format!("{iface} 没有可用的组网地址，请先启动组网隧道"))
}

#[derive(Clone, Deserialize)]
pub struct Options {
    pub port: u16,
    pub allow_lan: bool,
    #[serde(default)]
    pub auth_enabled: bool,
    pub username: String,
    pub password: String,
}

impl Options {
    pub fn validate(&self) -> Result<(), String> {
        if self.port == 0 {
            return Err("端口应为 1–65535".into());
        }
        if !self.auth_enabled {
            return Ok(());
        }
        if self.username.is_empty() || self.password.is_empty() {
            return Err("开启代理认证后，请填写用户名和密码".into());
        }
        if self.username.len() > 255
            || self.password.len() > 255
            || self.username.contains(':')
            || self
                .username
                .chars()
                .chain(self.password.chars())
                .any(char::is_control)
        {
            return Err("认证信息最长 255 字节，用户名不能包含冒号，不能包含控制字符".into());
        }
        if self.username.is_empty() != self.password.is_empty() {
            return Err("用户名和密码须同时填写或留空".into());
        }
        Ok(())
    }

    pub fn bind_address(&self) -> SocketAddr {
        SocketAddr::from((
            if self.allow_lan {
                Ipv4Addr::UNSPECIFIED
            } else {
                Ipv4Addr::LOCALHOST
            },
            self.port,
        ))
    }

    fn authenticated(&self) -> bool {
        self.auth_enabled
    }
}

#[derive(Default)]
pub struct Counters {
    active: AtomicU64,
    accepted: AtomicU64,
    rejected: AtomicU64,
    uploaded: AtomicU64,
    downloaded: AtomicU64,
}

#[derive(Clone, Default, Serialize)]
pub struct Status {
    pub running: bool,
    pub listen_address: String,
    pub authenticated: bool,
    pub active: u64,
    pub accepted: u64,
    pub rejected: u64,
    pub uploaded: u64,
    pub downloaded: u64,
}

impl Counters {
    pub fn status(&self, options: &Options, running: bool) -> Status {
        Status {
            running,
            listen_address: options.bind_address().to_string(),
            authenticated: options.authenticated(),
            active: self.active.load(Ordering::Relaxed),
            accepted: self.accepted.load(Ordering::Relaxed),
            rejected: self.rejected.load(Ordering::Relaxed),
            uploaded: self.uploaded.load(Ordering::Relaxed),
            downloaded: self.downloaded.load(Ordering::Relaxed),
        }
    }
}

struct Active(Arc<Counters>);
impl Drop for Active {
    fn drop(&mut self) {
        self.0.active.fetch_sub(1, Ordering::Relaxed);
    }
}

fn invalid(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

// 仅允许项目当前使用的组网网段，不扩大到全部局域网地址。
fn in_mesh(ip: IpAddr) -> bool {
    matches!(ip, IpAddr::V4(v) if v.octets()[0..2] == [172, 1])
}

async fn connect_mesh(host: &str, port: u16, route: MeshRoute) -> io::Result<TcpStream> {
    timeout(
        Duration::from_secs(8),
        connect_mesh_inner(host, port, route),
    )
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "mesh connect timeout"))?
}

async fn connect_mesh_inner(host: &str, port: u16, route: MeshRoute) -> io::Result<TcpStream> {
    if port == 0 {
        return Err(invalid("invalid port"));
    }
    // 域名仅解析一次，校验后直接连接该地址，避免检查与连接之间发生 DNS 重绑定。
    let addresses: Vec<_> = tokio::net::lookup_host((host, port)).await?.collect();
    if addresses.is_empty() || addresses.iter().any(|a| !route.allows(a.ip())) {
        return Err(invalid("target outside mesh"));
    }
    let mut last = invalid("no reachable mesh peer");
    for address in addresses {
        let socket = TcpSocket::new_v4()?;
        socket.bind(SocketAddr::from((route.source, 0)))?;
        match socket.connect(address).await {
            Ok(stream) => return Ok(stream),
            Err(e) => last = e,
        }
    }
    Err(last)
}

pub async fn serve(
    listener: TcpListener,
    options: Options,
    counters: Arc<Counters>,
    route: MeshRoute,
    mut stop: oneshot::Receiver<()>,
) -> io::Result<()> {
    let mut tasks = JoinSet::new();
    loop {
        tokio::select! {
            _ = &mut stop => {
                tasks.abort_all();
                while tasks.join_next().await.is_some() {}
                return Ok(());
            }
            connection = listener.accept() => {
                let (stream, _) = connection?;
                if tasks.len() >= 128 { counters.rejected.fetch_add(1, Ordering::Relaxed); continue; }
                let options = options.clone();
                let counters = counters.clone();
                counters.accepted.fetch_add(1, Ordering::Relaxed);
                counters.active.fetch_add(1, Ordering::Relaxed);
                let guard = Active(counters.clone());
                tasks.spawn(async move {
                    let _guard = guard;
                    if session(stream, &options, &counters, route).await.is_err() {
                        counters.rejected.fetch_add(1, Ordering::Relaxed);
                    }
                });
            }
            _ = tasks.join_next(), if !tasks.is_empty() => {}
        }
    }
    // 任务集合释放时会取消所有已接入连接，监听也随当前函数释放。
}

async fn session(
    mut client: TcpStream,
    options: &Options,
    counters: &Counters,
    route: MeshRoute,
) -> io::Result<()> {
    let handshake = async {
        let first = client.read_u8().await?;
        if first == 5 {
            socks(&mut client, options, route).await
        } else {
            http(&mut client, first, options, route).await
        }
    };
    let mut remote = timeout(Duration::from_secs(10), handshake)
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "proxy handshake timeout"))??;
    let (mut cr, mut cw) = client.split();
    let (mut rr, mut rw) = remote.split();
    tokio::try_join!(
        copy_counted(&mut cr, &mut rw, &counters.uploaded),
        copy_counted(&mut rr, &mut cw, &counters.downloaded)
    )?;
    Ok(())
}

async fn copy_counted<R: tokio::io::AsyncRead + Unpin, W: tokio::io::AsyncWrite + Unpin>(
    reader: &mut R,
    writer: &mut W,
    counter: &AtomicU64,
) -> io::Result<()> {
    let mut buf = [0u8; 16384];
    loop {
        let n = reader.read(&mut buf).await?;
        if n == 0 {
            return writer.shutdown().await;
        }
        writer.write_all(&buf[..n]).await?;
        counter.fetch_add(n as u64, Ordering::Relaxed);
    }
}

async fn socks(
    client: &mut TcpStream,
    options: &Options,
    route: MeshRoute,
) -> io::Result<TcpStream> {
    let n = client.read_u8().await? as usize;
    let mut methods = vec![0; n];
    client.read_exact(&mut methods).await?;
    let method = if options.authenticated() { 2 } else { 0 };
    if !methods.contains(&method) {
        client.write_all(&[5, 255]).await?;
        return Err(invalid("auth method"));
    }
    client.write_all(&[5, method]).await?;
    if method == 2 {
        let version = client.read_u8().await?;
        let username = read_short(client).await?;
        let password = read_short(client).await?;
        let ok = version == 1
            && username == options.username.as_bytes()
            && password == options.password.as_bytes();
        client.write_all(&[1, if ok { 0 } else { 1 }]).await?;
        if !ok {
            return Err(invalid("authentication failed"));
        }
    }
    let mut header = [0u8; 4];
    client.read_exact(&mut header).await?;
    if header[0] != 5 || header[1] != 1 || header[2] != 0 {
        client.write_all(&[5, 7, 0, 1, 0, 0, 0, 0, 0, 0]).await?;
        return Err(invalid("only SOCKS CONNECT supported"));
    }
    let host = match header[3] {
        1 => {
            let mut ip = [0; 4];
            client.read_exact(&mut ip).await?;
            Ipv4Addr::from(ip).to_string()
        }
        3 => String::from_utf8(read_short(client).await?).map_err(|_| invalid("invalid domain"))?,
        _ => {
            client.write_all(&[5, 8, 0, 1, 0, 0, 0, 0, 0, 0]).await?;
            return Err(invalid("address type"));
        }
    };
    let port = client.read_u16().await?;
    match connect_mesh(&host, port, route).await {
        Ok(remote) => {
            let local = remote.local_addr()?;
            let ip = match local.ip() {
                IpAddr::V4(ip) => ip.octets(),
                _ => [0; 4],
            };
            let mut reply = vec![5, 0, 0, 1];
            reply.extend_from_slice(&ip);
            reply.extend_from_slice(&local.port().to_be_bytes());
            client.write_all(&reply).await?;
            Ok(remote)
        }
        Err(e) => {
            client
                .write_all(&[
                    5,
                    if e.kind() == io::ErrorKind::PermissionDenied {
                        2
                    } else {
                        5
                    },
                    0,
                    1,
                    0,
                    0,
                    0,
                    0,
                    0,
                    0,
                ])
                .await?;
            Err(e)
        }
    }
}

async fn read_short(client: &mut TcpStream) -> io::Result<Vec<u8>> {
    let len = client.read_u8().await? as usize;
    let mut value = vec![0; len];
    client.read_exact(&mut value).await?;
    Ok(value)
}

async fn http_error(client: &mut TcpStream, status: &str) -> io::Result<()> {
    client
        .write_all(
            format!("HTTP/1.1 {status}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
                .as_bytes(),
        )
        .await
}

async fn http(
    client: &mut TcpStream,
    first: u8,
    options: &Options,
    route: MeshRoute,
) -> io::Result<TcpStream> {
    let mut bytes = vec![first];
    while !bytes.ends_with(b"\r\n\r\n") {
        if bytes.len() >= 16384 {
            http_error(client, "431 Request Header Fields Too Large").await?;
            return Err(invalid("header limit"));
        }
        bytes.push(client.read_u8().await?);
    }
    match http_request(&bytes, options) {
        Ok((host, port, request)) => match connect_mesh(&host, port, route).await {
            Ok(mut remote) => {
                if let Some(request) = request {
                    remote.write_all(request.as_bytes()).await?;
                } else {
                    client
                        .write_all(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                        .await?;
                }
                Ok(remote)
            }
            Err(e) => {
                http_error(
                    client,
                    if e.kind() == io::ErrorKind::PermissionDenied {
                        "403 Forbidden"
                    } else {
                        "502 Bad Gateway"
                    },
                )
                .await?;
                Err(e)
            }
        },
        Err(status) => {
            if status.starts_with("407") {
                client.write_all(b"HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"FileSync mesh\"\r\nConnection: close\r\nContent-Length: 0\r\n\r\n").await?;
            } else {
                http_error(client, status).await?;
            }
            Err(invalid("HTTP request rejected"))
        }
    }
}

fn http_request(
    bytes: &[u8],
    options: &Options,
) -> Result<(String, u16, Option<String>), &'static str> {
    let bad = "400 Bad Request";
    let text = std::str::from_utf8(bytes).map_err(|_| bad)?;
    let mut lines = text.split("\r\n");
    let parts: Vec<_> = lines.next().ok_or(bad)?.split_whitespace().collect();
    if parts.len() != 3
        || !matches!(parts[2], "HTTP/1.0" | "HTTP/1.1")
        || !parts[0].bytes().all(|b| b.is_ascii_uppercase())
    {
        return Err(bad);
    }
    let mut headers = Vec::new();
    let mut auth = None;
    for line in lines.take_while(|s| !s.is_empty()) {
        let (name, value) = line.split_once(':').ok_or(bad)?;
        if name.is_empty()
            || !name
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&b))
            || value.bytes().any(|b| b < 32 && b != b'\t')
        {
            return Err(bad);
        }
        if name.eq_ignore_ascii_case("proxy-authorization") {
            if auth.is_some() {
                return Err(bad);
            }
            auth = Some(value.trim());
        }
        headers.push((name, value.trim()));
    }
    if options.authenticated() {
        let expected = STANDARD.encode(format!("{}:{}", options.username, options.password));
        let provided = auth.and_then(|v| v.split_once(' '));
        if !matches!(provided, Some((scheme, value)) if scheme.eq_ignore_ascii_case("basic") && value == expected)
        {
            return Err("407 Proxy Authentication Required");
        }
    }
    let connect = parts[0] == "CONNECT";
    let url = url::Url::parse(&if connect {
        format!("http://{}", parts[1])
    } else {
        parts[1].to_string()
    })
    .map_err(|_| bad)?;
    if url.scheme() != "http"
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err(bad);
    }
    let host = url.host_str().ok_or(bad)?.to_string();
    let port = if connect {
        parts[1]
            .rsplit_once(':')
            .ok_or(bad)?
            .1
            .parse::<u16>()
            .map_err(|_| bad)?
    } else {
        url.port_or_known_default().ok_or(bad)?
    };
    if connect {
        if url.path() != "/" || url.query().is_some() {
            return Err(bad);
        }
        return Ok((host, port, None));
    }
    // 按单次请求转发并关闭连接复用，避免后续请求绕过目标检查。
    let mut request = format!(
        "{} {}{}{} HTTP/1.1\r\nHost: {}:{}\r\nConnection: close\r\n",
        parts[0],
        url.path(),
        if url.query().is_some() { "?" } else { "" },
        url.query().unwrap_or(""),
        host,
        port
    );
    for (name, value) in headers {
        if [
            "host",
            "proxy-authorization",
            "proxy-connection",
            "connection",
            "keep-alive",
        ]
        .iter()
        .any(|n| name.eq_ignore_ascii_case(n))
        {
            continue;
        }
        request.push_str(&format!("{name}: {value}\r\n"));
    }
    request.push_str("\r\n");
    Ok((host, port, Some(request)))
}

#[cfg(test)]
mod tests;
