use super::{bridge, Response};
use serde_json::Value;
use std::ffi::{CStr, CString};
use std::os::raw::c_char;
use std::panic::{catch_unwind, AssertUnwindSafe};

fn input_string(pointer: *const c_char) -> Result<String, String> {
    if pointer.is_null() {
        return Err("null input".into());
    }
    unsafe { CStr::from_ptr(pointer) }
        .to_str()
        .map(str::to_owned)
        .map_err(|e| e.to_string())
}

fn into_json<T: serde::Serialize>(value: T) -> *mut c_char {
    serde_json::to_string(&value)
        .ok()
        .and_then(|text| CString::new(text).ok())
        .map(|text| text.into_raw())
        .unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub extern "C" fn fc_upload_bridge_version() -> i32 {
    1
}

#[no_mangle]
pub extern "C" fn fc_upload_start(input: *const c_char) -> u64 {
    catch_unwind(AssertUnwindSafe(|| {
        let input = input_string(input)?;
        let start = serde_json::from_str::<bridge::Start>(&input).map_err(|e| e.to_string())?;
        bridge::start(start)
    }))
    .ok()
    .and_then(Result::ok)
    .unwrap_or(0)
}

#[no_mangle]
pub extern "C" fn fc_upload_poll(id: u64) -> *mut c_char {
    catch_unwind(AssertUnwindSafe(|| bridge::poll(id).map(into_json)))
        .ok()
        .and_then(Result::ok)
        .unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub extern "C" fn fc_upload_take_body(
    id: u64,
    request: u64,
    out_data: *mut *mut u8,
    out_len: *mut usize,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        if out_data.is_null() || out_len.is_null() {
            return -2;
        }
        unsafe {
            *out_data = std::ptr::null_mut();
            *out_len = 0;
        }
        let body = match bridge::take_body(id, request) {
            Ok(body) => body,
            Err(_) => return -1,
        };
        let len = body.len();
        let data = Box::into_raw(body.into_boxed_slice()) as *mut u8;
        unsafe {
            *out_data = data;
            *out_len = len;
        }
        0
    }))
    .unwrap_or(-1)
}

#[no_mangle]
pub extern "C" fn fc_upload_body_free(data: *mut u8, len: usize) {
    if data.is_null() {
        return;
    }
    let _ = catch_unwind(AssertUnwindSafe(|| unsafe {
        drop(Box::from_raw(std::ptr::slice_from_raw_parts_mut(data, len)));
    }));
}

#[no_mangle]
pub extern "C" fn fc_upload_reply(id: u64, request: u64, result: *const c_char) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        let result = match input_string(result)
            .and_then(|text| serde_json::from_str::<Value>(&text).map_err(|e| e.to_string()))
        {
            Ok(result) => result,
            Err(_) => return -2,
        };
        let reply = if let Some(error) = result.get("error").and_then(Value::as_str) {
            Err(error.to_string())
        } else if let Some(response) = result.get("response") {
            match serde_json::from_value::<Response<Value>>(response.clone()) {
                Ok(response) => Ok(response),
                Err(_) => return -2,
            }
        } else {
            return -2;
        };
        match bridge::reply(id, request, reply) {
            Ok(()) => 0,
            Err(_) => -1,
        }
    }))
    .unwrap_or(-1)
}

#[no_mangle]
pub extern "C" fn fc_upload_cancel(id: u64) {
    let _ = catch_unwind(AssertUnwindSafe(|| bridge::cancel(id)));
}
