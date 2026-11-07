//! Windows service bootstrap from a restricted, one-shot named pipe.
//!
//! Pure Rust implementation (no PowerShell): the WireGuard service is pointed at
//! a one-instance named pipe whose DACL only allows SYSTEM, Administrators and
//! the pipe owner. The configuration (including the private key) is written to
//! that pipe exactly once, so it never appears in a command line, environment
//! variable or file on disk.
use std::{
    ffi::c_void,
    os::windows::process::CommandExt,
    path::Path,
    process::{Child, Command, Stdio},
    ptr,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use windows_sys::Win32::{
    Foundation::{
        CloseHandle, GetLastError, LocalFree, ERROR_IO_PENDING, ERROR_PIPE_CONNECTED, HANDLE,
        INVALID_HANDLE_VALUE, WAIT_OBJECT_0,
    },
    Security::{
        Authorization::ConvertStringSecurityDescriptorToSecurityDescriptorW, PSECURITY_DESCRIPTOR,
        SECURITY_ATTRIBUTES,
    },
    Storage::FileSystem::{
        WriteFile, FILE_FLAG_FIRST_PIPE_INSTANCE, FILE_FLAG_OVERLAPPED, PIPE_ACCESS_OUTBOUND,
    },
    System::{
        Pipes::{
            ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_REJECT_REMOTE_CLIENTS,
            PIPE_TYPE_BYTE, PIPE_WAIT,
        },
        Services::{
            ChangeServiceConfigW, CloseServiceHandle, OpenSCManagerW, OpenServiceW,
            SC_MANAGER_CONNECT, SERVICE_CHANGE_CONFIG, SERVICE_DEMAND_START, SERVICE_NO_CHANGE,
        },
        Threading::{CreateEventW, WaitForSingleObject},
        IO::{GetOverlappedResult, OVERLAPPED},
    },
};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;

/// Create a one-shot outbound pipe whose DACL only grants SYSTEM, the built-in
/// Administrators group and the pipe owner (current user). No anonymous access.
fn create_secure_pipe(name: &str) -> Result<HANDLE, String> {
    const SDDL: &str = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)";
    let sddl: Vec<u16> = SDDL.encode_utf16().chain(std::iter::once(0)).collect();
    let mut descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
    let mut descriptor_size: u32 = 0;
    let converted = unsafe {
        ConvertStringSecurityDescriptorToSecurityDescriptorW(
            sddl.as_ptr(),
            1, // SDDL_REVISION_1
            &mut descriptor,
            &mut descriptor_size,
        )
    };
    if converted == 0 {
        return Err(format!(
            "无法构造内存管道安全描述符（错误 {}）",
            unsafe { GetLastError() }
        ));
    }
    let attributes = SECURITY_ATTRIBUTES {
        nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
        lpSecurityDescriptor: descriptor,
        bInheritHandle: 0,
    };
    let full_name: Vec<u16> = format!(r"\\.\pipe\{name}")
        .encode_utf16()
        .chain(std::iter::once(0))
        .collect();
    let pipe = unsafe {
        CreateNamedPipeW(
            full_name.as_ptr(),
            PIPE_ACCESS_OUTBOUND | FILE_FLAG_FIRST_PIPE_INSTANCE | FILE_FLAG_OVERLAPPED,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
            1,     // exactly one instance: the tunnel service gets this config only
            65536, // output buffer
            65536, // input buffer
            0,     // default timeout
            &attributes,
        )
    };
    unsafe {
        LocalFree(descriptor as *mut c_void);
    }
    if pipe == INVALID_HANDLE_VALUE || pipe.is_null() {
        return Err(format!("创建内存管道失败（错误 {}）", unsafe {
            GetLastError()
        }));
    }
    Ok(pipe)
}

fn create_event() -> Result<HANDLE, String> {
    let event = unsafe { CreateEventW(ptr::null(), 1, 0, ptr::null()) };
    if event.is_null() {
        return Err(format!("创建内存管道事件失败（错误 {}）", unsafe {
            GetLastError()
        }));
    }
    Ok(event)
}

fn wait_handle(handle: HANDLE, timeout_ms: u32) -> bool {
    unsafe { WaitForSingleObject(handle, timeout_ms) == WAIT_OBJECT_0 }
}

/// Wait until the WireGuard service opens the read side of the pipe.
fn wait_pipe_connected(pipe: HANDLE) -> Result<(), String> {
    let event = create_event()?;
    let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
    overlapped.hEvent = event;
    let connected = unsafe { ConnectNamedPipe(pipe, &mut overlapped) };
    let mut error = if connected == 0 {
        unsafe { GetLastError() }
    } else {
        0
    };
    if error == ERROR_PIPE_CONNECTED {
        // Client connected between CreateNamedPipeW and ConnectNamedPipe.
        error = 0;
    } else if error == ERROR_IO_PENDING {
        if wait_handle(event, 20_000) {
            let mut written: u32 = 0;
            let got = unsafe { GetOverlappedResult(pipe, &overlapped, &mut written, 0) };
            error = if got == 0 {
                unsafe { GetLastError() }
            } else {
                0
            };
        } else {
            unsafe {
                CloseHandle(event);
            }
            return Err("等待 WireGuard 服务连接内存管道超时（需要管理员权限）".into());
        }
    }
    unsafe {
        CloseHandle(event);
    }
    if error != 0 {
        return Err(format!(
            "等待 WireGuard 服务连接内存管道失败（错误 {error}，需要管理员权限）"
        ));
    }
    Ok(())
}

/// Write the configuration once; the pipe is closed by the caller afterwards so
/// the service sees EOF and finishes parsing.
fn write_pipe(pipe: HANDLE, data: &[u8]) -> Result<(), String> {
    let event = create_event()?;
    let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
    overlapped.hEvent = event;
    let mut written: u32 = 0;
    let ok = unsafe {
        WriteFile(
            pipe,
            data.as_ptr(),
            data.len() as u32,
            &mut written,
            &mut overlapped,
        )
    };
    let mut error = if ok == 0 {
        unsafe { GetLastError() }
    } else {
        0
    };
    if error == ERROR_IO_PENDING {
        if !wait_handle(event, 20_000) {
            unsafe {
                CloseHandle(event);
            }
            return Err("写入内存管道超时".into());
        }
        let mut transferred: u32 = 0;
        let got = unsafe { GetOverlappedResult(pipe, &overlapped, &mut transferred, 0) };
        error = if got == 0 {
            unsafe { GetLastError() }
        } else {
            0
        };
        written = transferred;
    }
    unsafe {
        CloseHandle(event);
    }
    if error != 0 || written as usize != data.len() {
        return Err(format!(
            "写入内存管道不完整（{written}/{} 字节，错误 {error}）",
            data.len()
        ));
    }
    Ok(())
}

fn wide(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0)).collect()
}

fn run_hidden(executable: &Path, args: &[&str]) -> std::io::Result<std::process::ExitStatus> {
    Command::new(executable)
        .args(args)
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
}

fn wait_installer(child: &mut Child, timeout: Duration) -> Result<(), String> {
    let started = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                if status.success() {
                    return Ok(());
                }
                return Err("WireGuard 服务安装失败（需要管理员权限）".into());
            }
            Ok(None) => {
                if started.elapsed() > timeout {
                    let _ = child.kill();
                    return Err("WireGuard 服务安装超时".into());
                }
                std::thread::sleep(Duration::from_millis(100));
            }
            Err(error) => return Err(format!("等待 WireGuard 服务安装结果失败：{error}")),
        }
    }
}

/// Switch the tunnel service to on-demand start: after a reboot the ephemeral
/// identity is gone, so the service must not try to restore from stale state.
fn set_service_demand_start(service: &str) -> Result<(), String> {
    let manager = unsafe { OpenSCManagerW(ptr::null(), ptr::null(), SC_MANAGER_CONNECT) };
    if manager.is_null() {
        return Err(format!(
            "打开服务控制管理器失败（错误 {}，需要管理员权限）",
            unsafe { GetLastError() }
        ));
    }
    let name = wide(service);
    let handle = unsafe { OpenServiceW(manager, name.as_ptr(), SERVICE_CHANGE_CONFIG) };
    if handle.is_null() {
        let error = unsafe { GetLastError() };
        unsafe {
            CloseServiceHandle(manager);
        }
        return Err(format!("打开隧道服务失败（错误 {error}）"));
    }
    let changed = unsafe {
        ChangeServiceConfigW(
            handle,
            SERVICE_NO_CHANGE,
            SERVICE_DEMAND_START,
            SERVICE_NO_CHANGE,
            ptr::null(),
            ptr::null(),
            ptr::null_mut(),
            ptr::null(),
            ptr::null(),
            ptr::null(),
            ptr::null(),
        )
    };
    let error = if changed == 0 {
        unsafe { GetLastError() }
    } else {
        0
    };
    unsafe {
        CloseServiceHandle(handle);
        CloseServiceHandle(manager);
    }
    if error != 0 {
        return Err(format!("设置隧道服务按需启动失败（错误 {error}）"));
    }
    Ok(())
}

fn verify_public_key(wg: &Path, interface: &str, expected: &str) -> Result<(), String> {
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        if let Ok(output) = Command::new(wg)
            .args(["show", interface, "public-key"])
            .creation_flags(CREATE_NO_WINDOW)
            .stdin(Stdio::null())
            .stderr(Stdio::null())
            .output()
        {
            if output.status.success() && String::from_utf8_lossy(&output.stdout).trim() == expected
            {
                return Ok(());
            }
        }
        if Instant::now() >= deadline {
            return Err("内存隧道公钥校验失败：服务未就绪或身份不一致".into());
        }
        std::thread::sleep(Duration::from_millis(250));
    }
}

/// Install (or replace) the WireGuard tunnel service and hand it its configuration
/// through a restricted in-memory pipe. The service command line contains only
/// the pipe name; the private key never touches disk, argv or the environment.
pub fn install_memory_tunnel(
    executable: &Path,
    interface: &str,
    public_key: &str,
    configuration: &str,
) -> Result<(), String> {
    if interface.is_empty()
        || interface.len() > 15
        || !interface
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    {
        return Err("Invalid tunnel interface".into());
    }
    if public_key.len() != 44
        || !public_key
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"+/=".contains(&b))
    {
        return Err("Invalid tunnel public key".into());
    }
    if !executable.is_file() {
        return Err(format!(
            "未找到内置 wireguard.exe：{}（请确认 resources/wireguard/ 随包分发）",
            executable.display()
        ));
    }
    // wg.exe ships next to wireguard.exe and is only used to verify the public
    // identity after the service is up.
    let wg = executable.with_file_name("wg.exe");
    if !wg.is_file() {
        return Err(format!(
            "未找到内置 wg.exe（应与 wireguard.exe 同目录）：{}",
            wg.display()
        ));
    }

    // Drop any previous tunnel first so a crashed session cannot pin the name.
    let _ = run_hidden(executable, &["/uninstalltunnelservice", interface]);

    let unique = format!(
        "{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
    );
    // The final path component determines the tunnel/interface name used by
    // WireGuard for Windows, hence "<unique>\<interface>.conf".
    let pipe_name = format!("filesync-memory-{unique}\\{interface}.conf");
    let pipe_path = format!(r"\\.\pipe\{pipe_name}");

    let pipe = create_secure_pipe(&pipe_name)?;
    struct PipeGuard(HANDLE);
    impl Drop for PipeGuard {
        fn drop(&mut self) {
            if !self.0.is_null() && self.0 != INVALID_HANDLE_VALUE {
                unsafe {
                    CloseHandle(self.0);
                }
            }
        }
    }
    let guard = PipeGuard(pipe);

    let mut installer = Command::new(executable)
        .args(["/installtunnelservice", &pipe_path])
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|error| format!("启动 WireGuard 服务安装程序失败：{error}（需要管理员权限）"))?;

    let delivered =
        wait_pipe_connected(guard.0).and_then(|_| write_pipe(guard.0, configuration.as_bytes()));
    // Close the pipe even on failure so the service cannot block forever.
    drop(guard);

    let outcome = delivered.and_then(|_| wait_installer(&mut installer, Duration::from_secs(20)));
    if let Err(error) = outcome {
        let _ = installer.kill();
        let _ = run_hidden(executable, &["/uninstalltunnelservice", interface]);
        return Err(format!("Windows 内存隧道启动失败：{error}"));
    }

    let service = format!("WireGuardTunnel${interface}");
    if let Err(error) = set_service_demand_start(&service) {
        let _ = run_hidden(executable, &["/uninstalltunnelservice", interface]);
        return Err(format!("Windows 内存隧道启动失败：{error}"));
    }
    if let Err(error) = verify_public_key(&wg, interface, public_key) {
        let _ = run_hidden(executable, &["/uninstalltunnelservice", interface]);
        return Err(format!("Windows 内存隧道启动失败：{error}"));
    }
    Ok(())
}
