// C ABI（JNI/cgo 同构使用）
//
// 边界两侧一律走 JSON 字符串（与 fc_sys_snapshot 同一风格），句柄不透明。
// 平台循环：fc_planner_next 拿 JSON 任务 → 执行 → fc_planner_report 回传 JSON 结果。

use std::ffi::CString;
use std::os::raw::{c_char, c_void};
use std::panic::{catch_unwind, AssertUnwindSafe};

use serde::Serialize;

use super::{Planner, PlannerInput, Report, Task};

fn cstr(p: *const c_char) -> Result<String, i32> {
    if p.is_null() {
        return Err(-10);
    }
    let s = unsafe { std::ffi::CStr::from_ptr(p) };
    s.to_str().map(|x| x.to_string()).map_err(|_| -11)
}

fn json_string(v: &impl Serialize) -> *mut c_char {
    match serde_json::to_string(v) {
        Ok(s) => CString::new(s)
            .map(|c| c.into_raw())
            .unwrap_or(std::ptr::null_mut()),
        Err(_) => std::ptr::null_mut(),
    }
}

fn planner_ref(h: *mut c_void) -> Result<&'static mut Planner, i32> {
    if h.is_null() {
        return Err(-1);
    }
    Ok(unsafe { &mut *(h as *mut Planner) })
}

/// 创建 Planner。desc_json = PlannerInput 序列化。失败返回 null。
#[no_mangle]
pub extern "C" fn fc_planner_new(desc_json: *const c_char) -> *mut c_void {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let s = match cstr(desc_json) {
            Ok(s) => s,
            Err(_) => return std::ptr::null_mut(),
        };
        match serde_json::from_str::<PlannerInput>(&s) {
            Ok(input) => match Planner::new(input) {
                Ok(p) => Box::into_raw(Box::new(p)) as *mut c_void,
                Err(_) => std::ptr::null_mut(),
            },
            Err(_) => std::ptr::null_mut(),
        }
    }));
    r.unwrap_or(std::ptr::null_mut())
}

/// 取下一个任务（JSON）。done/failed 是终态；recover 时等待 after_ms 后再次调用。
#[no_mangle]
pub extern "C" fn fc_planner_next(handle: *mut c_void) -> *mut c_char {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let p = match planner_ref(handle) {
            Ok(p) => p,
            Err(_) => {
                return json_string(&Task::Failed {
                    reason: "无效句柄".into(),
                })
            }
        };
        json_string(&p.next_task())
    }));
    r.unwrap_or_else(|_| {
        json_string(&Task::Failed {
            reason: "panic".into(),
        })
    })
}

/// 回传结果（JSON 的 Report）。返回 0 成功，负数失败。
#[no_mangle]
pub extern "C" fn fc_planner_report(handle: *mut c_void, report_json: *const c_char) -> i32 {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle)?;
        let s = cstr(report_json)?;
        let rep: Report = serde_json::from_str(&s).map_err(|_| -12)?;
        p.report(rep);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 会话过期重新接入。missing_json = [u32..]。返回 0 成功。
#[no_mangle]
pub extern "C" fn fc_planner_resume(
    handle: *mut c_void,
    upload_id: *const c_char,
    missing_json: *const c_char,
) -> i32 {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle)?;
        let id = cstr(upload_id)?;
        let s = cstr(missing_json)?;
        let missing: Vec<u32> = serde_json::from_str(&s).map_err(|_| -12)?;
        p.resume(&id, &missing);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 导出持久化状态（JSON 的 PersistedState）。平台负责落盘。
#[no_mangle]
pub extern "C" fn fc_planner_export(handle: *mut c_void) -> *mut c_char {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let p = match planner_ref(handle) {
            Ok(p) => p,
            Err(_) => return std::ptr::null_mut(),
        };
        json_string(&p.export_state())
    }));
    r.unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub extern "C" fn fc_planner_free(handle: *mut c_void) {
    if !handle.is_null() {
        let _ = catch_unwind(AssertUnwindSafe(|| unsafe {
            drop(Box::from_raw(handle as *mut Planner));
        }));
    }
}
