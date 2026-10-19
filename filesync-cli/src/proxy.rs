use anyhow::{anyhow, Context, Result};
use mesh_proxy::{Counters, MeshRoute, Options};
use std::{sync::Arc, time::Duration};
use tokio::{net::TcpListener, sync::oneshot};

pub fn run(port: u16, allow_lan: bool, username: Option<String>, iface: String) -> Result<()> {
    let options = Options {
        port,
        allow_lan,
        auth_enabled: username.is_some(),
        username: username.unwrap_or_default(),
        password: std::env::var("SYC_PROXY_PASSWORD").unwrap_or_default(),
    };
    options.validate().map_err(|e| anyhow!(e))?;
    let source = mesh_proxy::interface_address(&iface).map_err(|e| anyhow!(e))?;
    tokio::runtime::Runtime::new()?.block_on(async move {
        let listener = TcpListener::bind(options.bind_address()).await.context("监听代理端口失败")?;
        println!("HTTP/SOCKS5 代理监听 {}，经 {} ({source}) 访问 172.1.0.0/16；Ctrl+C 停止", listener.local_addr()?, iface);
        let counters = Arc::new(Counters::default());
        let (stop, stopped) = oneshot::channel();
        let server = mesh_proxy::serve(listener, options.clone(), counters.clone(), MeshRoute { source }, stopped);
        tokio::pin!(server);
        let mut tick = tokio::time::interval(Duration::from_secs(5));
        loop {
            tokio::select! {
                result = &mut server => return result.context("代理已停止"),
                signal = tokio::signal::ctrl_c() => {
                    signal.context("注册 Ctrl+C 失败")?;
                    let _ = stop.send(());
                    server.await?;
                    println!("代理已停止，监听与连接已释放");
                    return Ok(());
                }
                _ = tick.tick() => {
                    let s = counters.status(&options, true);
                    println!("连接 {} · 累计 {} · 失败/拒绝 {} · ↑ {} B ↓ {} B", s.active, s.accepted, s.rejected, s.uploaded, s.downloaded);
                }
            }
        }
    })
}
