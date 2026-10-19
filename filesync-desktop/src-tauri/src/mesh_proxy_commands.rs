use mesh_proxy::{Counters, Options, Status};
use std::sync::Arc;
use tauri::{Emitter, State};
use tokio::{
    net::TcpListener,
    sync::{oneshot, Mutex},
    task::JoinHandle,
};

struct Running {
    options: Options,
    counters: Arc<Counters>,
    task: JoinHandle<()>,
    stop: oneshot::Sender<()>,
}
#[derive(Default)]
pub struct MeshProxyState(Mutex<Option<Running>>);

#[tauri::command]
pub async fn mesh_proxy_status(state: State<'_, MeshProxyState>) -> Result<Status, String> {
    let guard = state.0.lock().await;
    Ok(guard
        .as_ref()
        .map(|r| r.counters.status(&r.options, !r.task.is_finished()))
        .unwrap_or_default())
}

#[tauri::command]
pub async fn mesh_proxy_start(
    app: tauri::AppHandle,
    options: Options,
    state: State<'_, MeshProxyState>,
) -> Result<Status, String> {
    options.validate()?;
    let source = tauri::async_runtime::spawn_blocking(|| mesh_proxy::interface_address("syc0"))
        .await
        .map_err(|e| e.to_string())??;
    let mut guard = state.0.lock().await;
    if guard.as_ref().is_some_and(|r| !r.task.is_finished()) {
        return Err("代理已运行，请先停止后修改配置".into());
    }
    // 先绑定端口，发生冲突时立即报错，避免界面误报已运行。
    let listener = TcpListener::bind(options.bind_address())
        .await
        .map_err(|e| format!("监听代理端口失败: {e}"))?;
    let counters = Arc::new(Counters::default());
    let status = counters.status(&options, true);
    let task_options = options.clone();
    let task_counters = counters.clone();
    let (stop, stopped) = oneshot::channel();
    let task = tokio::spawn(async move {
        let server = mesh_proxy::serve(
            listener,
            task_options.clone(),
            task_counters.clone(),
            mesh_proxy::MeshRoute { source },
            stopped,
        );
        tokio::pin!(server);
        let mut tick = tokio::time::interval(std::time::Duration::from_secs(1));
        loop {
            tokio::select! {
                result = &mut server => {
                    if let Err(e) = result { crate::logger::warn("mesh_proxy", format!("代理监听停止: {e}")); }
                    let _ = app.emit("mesh-proxy-status", task_counters.status(&task_options, false));
                    break;
                }
                _ = tick.tick() => { let _ = app.emit("mesh-proxy-status", task_counters.status(&task_options, true)); }
            }
        }
    });
    *guard = Some(Running {
        options,
        counters,
        task,
        stop,
    });
    Ok(status)
}

#[tauri::command]
pub async fn mesh_proxy_stop(
    app: tauri::AppHandle,
    state: State<'_, MeshProxyState>,
) -> Result<Status, String> {
    let mut guard = state.0.lock().await;
    if let Some(running) = guard.take() {
        let _ = running.stop.send(());
        let _ = running.task.await;
    }
    let status = Status::default();
    let _ = app.emit("mesh-proxy-status", &status);
    Ok(status)
}
