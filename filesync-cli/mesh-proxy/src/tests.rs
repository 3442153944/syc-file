use super::*;

fn options(auth: bool) -> Options {
    Options {
        port: 7897,
        allow_lan: auth,
        auth_enabled: auth,
        username: if auth { "mesh" } else { "" }.into(),
        password: if auth { "test-pass" } else { "" }.into(),
    }
}

async fn proxy(
    auth: bool,
) -> (
    SocketAddr,
    tokio::task::JoinHandle<io::Result<()>>,
    Arc<Counters>,
    oneshot::Sender<()>,
) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let counters = Arc::new(Counters::default());
    let (stop, stopped) = oneshot::channel();
    let task = tokio::spawn(serve(
        listener,
        options(auth),
        counters.clone(),
        MeshRoute {
            source: Ipv4Addr::LOCALHOST,
        },
        stopped,
    ));
    (addr, task, counters, stop)
}

async fn header(client: &mut TcpStream) -> String {
    timeout(Duration::from_secs(2), async {
        let mut bytes = Vec::new();
        while !bytes.ends_with(b"\r\n\r\n") {
            bytes.push(client.read_u8().await.unwrap());
        }
        String::from_utf8(bytes).unwrap()
    })
    .await
    .unwrap()
}

async fn echo() -> (SocketAddr, tokio::task::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let task = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.unwrap();
        let (mut reader, mut writer) = stream.split();
        tokio::io::copy(&mut reader, &mut writer).await.unwrap();
    });
    (addr, task)
}

#[test]
fn mesh_and_lan_boundaries() {
    assert!(in_mesh("172.1.0.254".parse().unwrap()));
    for ip in [
        "172.2.0.1",
        "127.0.0.1",
        "192.168.1.1",
        "8.8.8.8",
        "::1",
        "::ffff:172.1.0.1",
    ] {
        assert!(!in_mesh(ip.parse().unwrap()));
    }
    let mut config = options(false);
    assert!(config.validate().is_ok());
    assert!(config.bind_address().ip().is_loopback());
    config.allow_lan = true;
    assert!(config.validate().is_ok());
    assert!(!config.authenticated());
    config.username = "unused".into();
    assert!(!config.authenticated());
    assert!(config.validate().is_ok());
    config.auth_enabled = true;
    assert!(config.validate().is_err());
    assert!(options(true).validate().is_ok());
    config = options(true);
    config.port = 0;
    assert!(config.validate().is_err());
}

#[test]
fn http_rewrites_and_does_not_leak_proxy_auth() {
    let auth = STANDARD.encode("mesh:test-pass");
    let request = format!("GET http://172.1.0.254:8080/a?q=b HTTP/1.1\r\nHost: attacker\r\nProxy-Authorization: Basic {auth}\r\nConnection: keep-alive\r\n\r\n");
    let (host, port, request) = http_request(request.as_bytes(), &options(true)).unwrap();
    assert_eq!((host.as_str(), port), ("172.1.0.254", 8080));
    let request = request.unwrap();
    assert!(request.starts_with("GET /a?q=b HTTP/1.1\r\nHost: 172.1.0.254:8080\r\n"));
    assert!(!request.to_lowercase().contains("proxy-authorization"));
    assert!(!request.contains("attacker"));
    assert!(request.contains("Connection: close"));
    assert!(http_request(b"CONNECT 172.1.0.254:80 HTTP/1.1\r\n\r\n", &options(false)).is_ok());
    assert!(http_request(b"GET /relative HTTP/1.1\r\n\r\n", &options(false)).is_err());
}

#[tokio::test]
async fn http_connect_streams_and_stop_closes_connections() {
    let (remote, echo_task) = echo().await;
    let (addr, task, counters, stop) = proxy(false).await;
    let mut client = TcpStream::connect(addr).await.unwrap();
    client
        .write_all(format!("CONNECT {remote} HTTP/1.1\r\n\r\n").as_bytes())
        .await
        .unwrap();
    assert!(header(&mut client).await.starts_with("HTTP/1.1 200"));
    client.write_all(b"mesh-payload").await.unwrap();
    let mut reply = [0; 12];
    client.read_exact(&mut reply).await.unwrap();
    assert_eq!(&reply, b"mesh-payload");
    assert_eq!(counters.uploaded.load(Ordering::Relaxed), 12);
    stop.send(()).unwrap();
    task.await.unwrap().unwrap();
    let result = timeout(Duration::from_secs(2), client.read_u8())
        .await
        .unwrap();
    assert!(result.is_err());
    timeout(Duration::from_secs(2), echo_task)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(counters.active.load(Ordering::Relaxed), 0);
    // 停止后必须释放监听，确保同一端口可以重新启动。
    TcpListener::bind(addr).await.unwrap();
}

#[tokio::test]
async fn socks_authenticated_connect_and_half_close() {
    let (remote, echo_task) = echo().await;
    let (addr, task, counters, _stop) = proxy(true).await;
    let mut client = TcpStream::connect(addr).await.unwrap();
    client.write_all(&[5, 2, 0, 2]).await.unwrap();
    let mut two = [0; 2];
    client.read_exact(&mut two).await.unwrap();
    assert_eq!(two, [5, 2]);
    client
        .write_all(b"\x01\x04mesh\x09test-pass")
        .await
        .unwrap();
    client.read_exact(&mut two).await.unwrap();
    assert_eq!(two, [1, 0]);
    let mut request = vec![5, 1, 0, 1, 127, 0, 0, 1];
    request.extend_from_slice(&remote.port().to_be_bytes());
    client.write_all(&request).await.unwrap();
    let mut reply = [0; 10];
    client.read_exact(&mut reply).await.unwrap();
    assert_eq!(reply[1], 0);
    client.write_all(b"socks-data").await.unwrap();
    client.shutdown().await.unwrap();
    let mut echoed = Vec::new();
    timeout(Duration::from_secs(2), client.read_to_end(&mut echoed))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(echoed, b"socks-data");
    assert_eq!(counters.downloaded.load(Ordering::Relaxed), 10);
    echo_task.await.unwrap();
    task.abort();
}

#[tokio::test]
async fn failed_auth_and_non_mesh_destinations_are_rejected() {
    let (addr, task, _, _stop) = proxy(true).await;
    let mut client = TcpStream::connect(addr).await.unwrap();
    client
        .write_all(b"CONNECT 172.1.0.254:80 HTTP/1.1\r\n\r\n")
        .await
        .unwrap();
    assert!(header(&mut client).await.starts_with("HTTP/1.1 407"));
    let mut client = TcpStream::connect(addr).await.unwrap();
    client.write_all(&[5, 1, 0]).await.unwrap();
    let mut two = [0; 2];
    client.read_exact(&mut two).await.unwrap();
    assert_eq!(two, [5, 255]);
    let mut client = TcpStream::connect(addr).await.unwrap();
    client.write_all(&[5, 1, 2]).await.unwrap();
    client.read_exact(&mut two).await.unwrap();
    client.write_all(b"\x01\x04mesh\x03bad").await.unwrap();
    client.read_exact(&mut two).await.unwrap();
    assert_eq!(two, [1, 1]);
    task.abort();
    let (addr, task, _, _stop) = proxy(false).await;
    let mut client = TcpStream::connect(addr).await.unwrap();
    client
        .write_all(b"CONNECT 192.168.1.1:80 HTTP/1.1\r\n\r\n")
        .await
        .unwrap();
    assert!(header(&mut client).await.starts_with("HTTP/1.1 403"));
    let mut client = TcpStream::connect(addr).await.unwrap();
    client.write_all(&[5, 1, 0]).await.unwrap();
    client.read_exact(&mut two).await.unwrap();
    client
        .write_all(&[5, 1, 0, 1, 8, 8, 8, 8, 0, 80])
        .await
        .unwrap();
    let mut reply = [0; 10];
    client.read_exact(&mut reply).await.unwrap();
    assert_eq!(reply[1], 2);
    task.abort();
}

#[tokio::test]
async fn plain_http_forwarding_preserves_response() {
    let remote = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = remote.local_addr().unwrap();
    let remote_task = tokio::spawn(async move {
        let (mut stream, _) = remote.accept().await.unwrap();
        assert!(header(&mut stream)
            .await
            .starts_with("GET /resource HTTP/1.1"));
        stream
            .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\nmesh")
            .await
            .unwrap();
    });
    let (addr, task, _, _stop) = proxy(false).await;
    let mut client = TcpStream::connect(addr).await.unwrap();
    client
        .write_all(format!("GET http://{address}/resource HTTP/1.1\r\n\r\n").as_bytes())
        .await
        .unwrap();
    assert!(header(&mut client).await.starts_with("HTTP/1.1 200 OK"));
    let mut body = [0; 4];
    client.read_exact(&mut body).await.unwrap();
    assert_eq!(&body, b"mesh");
    remote_task.await.unwrap();
    task.abort();
}
