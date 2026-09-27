// ws_client/io.rs
// 职责：一条 WS 连接上的读写循环——转发入站文本帧、发送出站帧、客户端心跳、死连接检测。
// 纯 I/O，不碰 Tauri/业务类型，所以能直接拿本地 WS 服务端做单测。
//
// 为什么需要客户端心跳 + 判死：
//   服务端 54 秒才 ping 一次，客户端以前只回 pong、自己不发任何东西，读也没有超时。
//   防火墙/NAT 静默丢包（不发 RST）时，连接在网络上已经死了，这边却永远等在 ws.next() 上，
//   界面一直显示「已连接」，再也收不到任何推送，也不会重连。
//   现在：每 heartbeat 发一帧应用层心跳（既保持防火墙里的连接状态不过期，服务端还会回 ack），
//   超过 dead_after 没收到服务端任何数据就判死，交给外层重连。
use futures_util::{Sink, SinkExt, Stream, StreamExt};
use std::future::Future;
use std::time::Duration;
use tokio::sync::mpsc;
use tokio::time::{interval_at, timeout, Instant, MissedTickBehavior};
use tokio_tungstenite::tungstenite::{Error as WsError, Message};

/// 心跳/判死参数。
#[derive(Clone, Copy, Debug)]
pub struct Timing {
    /// 客户端主动发心跳的间隔。
    pub heartbeat: Duration,
    /// 超过这么久没收到服务端任何一帧，就判定连接已死。
    /// 必须大于服务端 ping 周期（54s）：老服务端不认心跳、不回 ack 时，只靠它的 ping 续命。
    pub dead_after: Duration,
    /// 单次写入的超时：对端不读、发送缓冲塞满时不能永远卡在写上。
    pub write_timeout: Duration,
}

impl Timing {
    pub const DEFAULT: Timing = Timing {
        heartbeat: Duration::from_secs(20),
        dead_after: Duration::from_secs(75),
        write_timeout: Duration::from_secs(15),
    };
}

/// 应用层心跳帧。选它而不是 WS 层 ping：服务端 handleHeartbeat 会刷新 LastHeartbeat 并回 ack，
/// 是端到端的存活证明；WS 层 ping/pong 可能被中间的 WS 代理就地应答，证明不了对面还活着。
pub const HEARTBEAT_FRAME: &str = r#"{"type":"heartbeat"}"#;

#[derive(Debug, PartialEq, Eq)]
pub enum EndReason {
    /// 对端关闭，或读到错误
    Closed,
    /// 太久没收到任何数据（防火墙静默丢包 / 网络断了但没人告诉我们）
    Dead,
    /// 往连接里写失败或写超时
    WriteFailed,
    /// 节点被切走，主动断开
    Switched,
}

/// 跑到连接结束，返回结束原因。
///
/// - `on_text`：每收到一条文本帧同步调用一次。必须轻量、不阻塞（重活自己丢去别的任务），
///   否则会占着读循环，心跳和 pong 都发不出去。
/// - `switched`：节点被切走时完成的 future（生产环境传 `net::wait_switch(epoch)`）。
pub async fn run<S, F>(
    ws: &mut S,
    out_rx: &mut mpsc::UnboundedReceiver<Message>,
    timing: Timing,
    switched: impl Future<Output = ()>,
    mut on_text: F,
) -> EndReason
where
    S: Stream<Item = Result<Message, WsError>> + Sink<Message> + Unpin,
    F: FnMut(&str),
{
    // 第一次心跳在连上 heartbeat 之后才发，不是连上就发
    let mut hb = interval_at(Instant::now() + timing.heartbeat, timing.heartbeat);
    hb.set_missed_tick_behavior(MissedTickBehavior::Delay);
    let mut last_rx = Instant::now();
    let mut out_open = true;
    tokio::pin!(switched);

    loop {
        tokio::select! {
            inbound = ws.next() => {
                last_rx = Instant::now(); // 任何入站帧（含 ping/pong/ack）都算活着
                match inbound {
                    Some(Ok(Message::Text(t))) => on_text(&t),
                    Some(Ok(Message::Ping(d))) => {
                        if !send(ws, Message::Pong(d), timing.write_timeout).await {
                            return EndReason::WriteFailed;
                        }
                    }
                    Some(Ok(Message::Close(_))) | Some(Err(_)) | None => return EndReason::Closed,
                    Some(Ok(_)) => {}
                }
            }
            outbound = out_rx.recv(), if out_open => {
                match outbound {
                    Some(frame) => {
                        if !send(ws, frame, timing.write_timeout).await {
                            return EndReason::WriteFailed;
                        }
                    }
                    // 发送端全部 drop（静态持有，理论上不会发生）：关掉这一路，别让 recv 立刻返回 None 空转
                    None => out_open = false,
                }
            }
            _ = hb.tick() => {
                if last_rx.elapsed() >= timing.dead_after {
                    return EndReason::Dead;
                }
                if !send(ws, Message::Text(HEARTBEAT_FRAME.into()), timing.write_timeout).await {
                    return EndReason::WriteFailed;
                }
            }
            // 节点被切走：这条连接打在旧地址上，继续留着只会让同步消息进错入口
            _ = &mut switched => {
                let _ = send(ws, Message::Close(None), timing.write_timeout).await;
                return EndReason::Switched;
            }
        }
    }
}

async fn send<S>(ws: &mut S, msg: Message, write_timeout: Duration) -> bool
where
    S: Sink<Message> + Unpin,
{
    matches!(timeout(write_timeout, ws.send(msg)).await, Ok(Ok(())))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Arc, Mutex};
    use tokio::net::TcpListener;
    use tokio_tungstenite::{accept_async, connect_async};

    const FAST: Timing = Timing {
        heartbeat: Duration::from_millis(50),
        dead_after: Duration::from_millis(300),
        write_timeout: Duration::from_secs(1),
    };

    /// 起一个本地 WS 服务端，每个连接交给 handler；返回客户端连上去的 ws://。
    async fn serve<H, Fut>(handler: H) -> String
    where
        H: FnOnce(tokio_tungstenite::WebSocketStream<tokio::net::TcpStream>) -> Fut + Send + 'static,
        Fut: Future<Output = ()> + Send + 'static,
    {
        let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("ws://127.0.0.1:{}", l.local_addr().unwrap().port());
        tokio::spawn(async move {
            let (s, _) = l.accept().await.unwrap();
            handler(accept_async(s).await.unwrap()).await;
        });
        url
    }

    fn never() -> impl Future<Output = ()> {
        std::future::pending::<()>()
    }

    #[tokio::test]
    async fn sends_heartbeats_periodically() {
        let (tx, mut rx) = mpsc::unbounded_channel::<String>();
        let url = serve(move |mut ws| async move {
            while let Some(Ok(m)) = ws.next().await {
                if let Message::Text(t) = m {
                    let _ = tx.send(t);
                }
            }
        })
        .await;
        let (mut ws, _) = connect_async(&url).await.unwrap();
        let (_out_tx, mut out_rx) = mpsc::unbounded_channel();
        let timing = Timing { dead_after: Duration::from_secs(30), ..FAST };
        // 应当一直跑下去，这里只观察 400ms
        let r = timeout(Duration::from_millis(400), run(&mut ws, &mut out_rx, timing, never(), |_| {})).await;
        assert!(r.is_err(), "会话不该自己结束: {:?}", r);
        drop(ws);
        let mut n = 0;
        while let Ok(t) = rx.try_recv() {
            assert_eq!(t, HEARTBEAT_FRAME);
            n += 1;
        }
        assert!(n >= 3, "400ms 内只收到 {} 条心跳", n);
    }

    #[tokio::test]
    async fn silent_peer_is_declared_dead() {
        // 服务端握完手就装死：不读、不写、不关，等价于防火墙静默丢包后的连接
        let url = serve(|ws| async move {
            let _keep = ws;
            tokio::time::sleep(Duration::from_secs(10)).await;
        })
        .await;
        let (mut ws, _) = connect_async(&url).await.unwrap();
        let (_out_tx, mut out_rx) = mpsc::unbounded_channel();
        let started = std::time::Instant::now();
        let r = timeout(Duration::from_secs(3), run(&mut ws, &mut out_rx, FAST, never(), |_| {}))
            .await
            .expect("判死应当在 3s 内发生");
        assert_eq!(r, EndReason::Dead);
        assert!(started.elapsed() >= FAST.dead_after, "判死太早: {:?}", started.elapsed());
    }

    #[tokio::test]
    async fn forwards_text_replies_pong_and_reports_close() {
        let (pong_tx, mut pong_rx) = mpsc::unbounded_channel::<Vec<u8>>();
        let url = serve(move |mut ws| async move {
            ws.send(Message::Text("a".into())).await.unwrap();
            ws.send(Message::Ping(vec![1, 2])).await.unwrap();
            while let Some(Ok(m)) = ws.next().await {
                if let Message::Pong(d) = m {
                    let _ = pong_tx.send(d);
                    break;
                }
            }
            ws.send(Message::Close(None)).await.ok();
        })
        .await;
        let (mut ws, _) = connect_async(&url).await.unwrap();
        let (_out_tx, mut out_rx) = mpsc::unbounded_channel();
        let got = Arc::new(Mutex::new(Vec::<String>::new()));
        let g = got.clone();
        let r = timeout(
            Duration::from_secs(3),
            run(&mut ws, &mut out_rx, Timing { dead_after: Duration::from_secs(30), ..FAST }, never(), move |t| {
                g.lock().unwrap().push(t.to_string())
            }),
        )
        .await
        .unwrap();
        assert_eq!(r, EndReason::Closed);
        assert_eq!(*got.lock().unwrap(), vec!["a".to_string()]);
        assert_eq!(pong_rx.try_recv().unwrap(), vec![1, 2]);
    }

    #[tokio::test]
    async fn outbound_frames_reach_the_server() {
        let (tx, mut rx) = mpsc::unbounded_channel::<String>();
        let url = serve(move |mut ws| async move {
            while let Some(Ok(m)) = ws.next().await {
                if let Message::Text(t) = m {
                    if t != HEARTBEAT_FRAME {
                        let _ = tx.send(t);
                        break;
                    }
                }
            }
            ws.send(Message::Close(None)).await.ok();
        })
        .await;
        let (mut ws, _) = connect_async(&url).await.unwrap();
        let (out_tx, mut out_rx) = mpsc::unbounded_channel();
        out_tx.send(Message::Text("subscribe".into())).unwrap();
        let timing = Timing { dead_after: Duration::from_secs(30), ..FAST };
        let r = timeout(Duration::from_secs(3), run(&mut ws, &mut out_rx, timing, never(), |_| {}))
            .await
            .unwrap();
        assert_eq!(r, EndReason::Closed);
        assert_eq!(rx.try_recv().unwrap(), "subscribe");
    }

    #[tokio::test]
    async fn switch_signal_closes_the_connection() {
        let (tx, mut rx) = mpsc::unbounded_channel::<()>();
        let url = serve(move |mut ws| async move {
            while let Some(Ok(m)) = ws.next().await {
                if m.is_close() {
                    let _ = tx.send(());
                    break;
                }
            }
        })
        .await;
        let (mut ws, _) = connect_async(&url).await.unwrap();
        let (_out_tx, mut out_rx) = mpsc::unbounded_channel();
        let timing = Timing { dead_after: Duration::from_secs(30), ..FAST };
        let r = timeout(
            Duration::from_secs(3),
            run(&mut ws, &mut out_rx, timing, tokio::time::sleep(Duration::from_millis(100)), |_| {}),
        )
        .await
        .unwrap();
        assert_eq!(r, EndReason::Switched);
        assert!(timeout(Duration::from_secs(2), rx.recv()).await.unwrap().is_some(), "服务端没收到 Close 帧");
    }
}
