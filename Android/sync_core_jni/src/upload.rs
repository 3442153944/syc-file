//! JNI marshaling only; the upload runtime and state machine live in sys_core.
use jni::{
    objects::{JObject, JString},
    sys::{jbyteArray, jlong, jstring},
    JNIEnv,
};
use std::panic::{catch_unwind, AssertUnwindSafe};
use sys_core::upload::bridge;

#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativeBridgeVersion(
    _env: JNIEnv,
    _obj: JObject,
) -> jni::sys::jint {
    1
}

fn string(env: &mut JNIEnv, value: &JString) -> Result<String, String> {
    env.get_string(value)
        .map(Into::into)
        .map_err(|e| e.to_string())
}
fn guard<T: Default>(env: &mut JNIEnv, f: impl FnOnce(&mut JNIEnv) -> Result<T, String>) -> T {
    match catch_unwind(AssertUnwindSafe(|| f(env))) {
        Ok(Ok(value)) => value,
        result => {
            let message = match result {
                Ok(Err(e)) => e,
                _ => "sys_core upload panic".into(),
            };
            let _ = env.throw_new("java/lang/IllegalStateException", message);
            T::default()
        }
    }
}
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativeStart(
    mut env: JNIEnv,
    _obj: JObject,
    input: JString,
) -> jlong {
    guard(&mut env, |env| {
        let input = serde_json::from_str(&string(env, &input)?).map_err(|e| e.to_string())?;
        bridge::start(input).map(|id| id as jlong)
    })
}
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativePoll(
    mut env: JNIEnv,
    _obj: JObject,
    job: jlong,
) -> jstring {
    guard(&mut env, |env| {
        let events = bridge::poll(job as u64)?;
        env.new_string(events.to_string())
            .map(|s| s.into_raw())
            .map_err(|e| e.to_string())
    })
}
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativeTakeBody(
    mut env: JNIEnv,
    _obj: JObject,
    job: jlong,
    request: jlong,
) -> jbyteArray {
    guard(&mut env, |env| {
        let bytes = bridge::take_body(job as u64, request as u64)?;
        env.byte_array_from_slice(&bytes)
            .map(|s| s.into_raw())
            .map_err(|e| e.to_string())
    })
}
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativeReply(
    mut env: JNIEnv,
    _obj: JObject,
    job: jlong,
    request: jlong,
    result: JString,
) {
    guard(&mut env, |env| {
        let value: serde_json::Value =
            serde_json::from_str(&string(env, &result)?).map_err(|e| e.to_string())?;
        let response = if let Some(error) = value.get("error").and_then(|v| v.as_str()) {
            Err(error.to_owned())
        } else {
            Ok(
                serde_json::from_value(value.get("response").cloned().ok_or("missing response")?)
                    .map_err(|e| e.to_string())?,
            )
        };
        bridge::reply(job as u64, request as u64, response)
    });
}
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SysUpload_nativeCancel(
    mut env: JNIEnv,
    _obj: JObject,
    job: jlong,
) {
    guard(&mut env, |_| {
        bridge::cancel(job as u64);
        Ok(())
    });
}
