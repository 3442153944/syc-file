#![cfg(feature = "upload")]
use serde_json::{json, Value};
use std::{
    path::PathBuf,
    time::{Duration, Instant},
};
use sys_core::upload::{bridge, Response};
struct Job {
    id: u64,
    file: PathBuf,
}
impl Drop for Job {
    fn drop(&mut self) {
        bridge::cancel(self.id);
        let _ = std::fs::remove_file(&self.file);
    }
}
fn job(resume: Value) -> Job {
    let file = std::env::temp_dir().join(format!("sys-core-bridge-{}", uuid::Uuid::new_v4()));
    std::fs::write(&file, b"abcdefghij").unwrap();
    let input=serde_json::from_value(json!({"file":file,"remote_dir":"/test","base_url":"http://unit.invalid", "chunk_size":4,"concurrency":2,"resume":resume})).unwrap();
    Job {
        id: bridge::start(input).unwrap(),
        file,
    }
}
fn reply(id: u64, request: u64, data: Value, code: i32) {
    bridge::reply(
        id,
        request,
        Ok(Response {
            code,
            message: String::new(),
            data: Some(data),
        }),
    )
    .unwrap();
}
#[test]
fn bridge_runs_shared_fixed_upload_and_reinitializes_expired_session() {
    let job = job(Value::Null);
    let mut init = 0;
    let mut chunks = 0;
    let mut expired = false;
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        assert!(Instant::now() < deadline, "bridge did not complete");
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    let id = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/file/upload/init" => {
                            init += 1;
                            assert_eq!(event["body"]["value"]["chunk_count"], 3);
                            reply(
                                job.id,
                                id,
                                json!({"upload_id":format!("session-{init}"),"missing":[0,1,2]}),
                                200,
                            );
                        }
                        "/file/upload/chunk" => {
                            let index = event["query"]
                                .as_array()
                                .unwrap()
                                .iter()
                                .find(|p| p[0] == "index")
                                .unwrap()[1]
                                .as_str()
                                .unwrap()
                                .parse::<usize>()
                                .unwrap();
                            let Ok(data) = bridge::take_body(job.id, id) else {
                                continue;
                            };
                            assert_eq!(data, &b"abcdefghij"[index * 4..(index * 4 + 4).min(10)]);
                            chunks += 1;
                            if !expired {
                                expired = true;
                                reply(job.id, id, Value::Null, 404);
                            } else {
                                reply(job.id, id, json!({}), 200);
                            }
                        }
                        "/file/upload/complete" => reply(
                            job.id,
                            id,
                            json!({"file_name":"file","storage_path":"/test/file","file_size":10}),
                            200,
                        ),
                        path => panic!("unexpected {path}"),
                    }
                }
                "done" => {
                    assert_eq!(init, 2);
                    assert!(chunks >= 4);
                    assert_eq!(event["data"]["file_size"], 10);
                    return;
                }
                "failed" => panic!("{event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}
#[test]
fn resumed_session_preserves_chunk_size_and_emits_session_callback() {
    let job = job(json!({"upload_id":"resumed","chunk_size":3,"chunk_count":4,"missing":[3]}));
    let deadline = Instant::now() + Duration::from_secs(5);
    let mut session = false;
    let mut count = 0;
    loop {
        assert!(Instant::now() < deadline);
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "session" => {
                    assert_eq!(event["upload_id"], "resumed");
                    session = true;
                }
                "request" => {
                    let id = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/file/upload/init" => panic!("resumed session must not init twice"),
                        "/file/upload/chunk" => {
                            assert_eq!(bridge::take_body(job.id, id).unwrap(), b"j");
                            count += 1;
                            reply(job.id, id, json!({}), 200);
                        }
                        "/file/upload/complete" => reply(
                            job.id,
                            id,
                            json!({"file_name":"file","storage_path":"/test/file","file_size":10}),
                            200,
                        ),
                        path => panic!("unexpected {path}"),
                    }
                }
                "done" => {
                    assert!(session);
                    assert_eq!(count, 1);
                    return;
                }
                "failed" => panic!("{event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}
#[test]
fn cancelled_handle_rejects_polling_and_late_replies() {
    let job = job(Value::Null);
    bridge::cancel(job.id);
    assert!(bridge::poll(job.id).is_err());
    assert!(bridge::take_body(job.id, 1).is_err());
    assert!(bridge::reply(job.id, 1, Err("late response".into())).is_err());
    bridge::cancel(job.id);
}

#[test]
fn empty_missing_list_completes_without_resending_late_chunks() {
    let job = job(json!({"upload_id":"all-received","chunk_size":4,"chunk_count":3,"missing":[]}));
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        assert!(Instant::now() < deadline);
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    assert_eq!(event["path"], "/file/upload/complete");
                    reply(
                        job.id,
                        event["id"].as_u64().unwrap(),
                        json!({"file_name":"file","storage_path":"/test/file","file_size":10}),
                        200,
                    );
                }
                "done" => return,
                "failed" => panic!("{event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}
#[test]
fn adaptive_quick_share_uses_shared_loop_and_checks_download_entry() {
    let file = std::env::temp_dir().join(format!("sys-core-adaptive-{}", uuid::Uuid::new_v4()));
    std::fs::write(&file, b"abcdefghij").unwrap();
    let input=serde_json::from_value(json!({"file":file,"remote_dir":"","base_url":"http://unit.invalid", "quick_share":true,"main_node":"active","nodes":[{"id":"active","url":"http://unit.invalid"}]})).unwrap();
    let job = Job {
        id: bridge::start(input).unwrap(),
        file,
    };
    let deadline = Instant::now() + Duration::from_secs(5);
    let mut chunk = false;
    let mut probe = false;
    loop {
        assert!(Instant::now() < deadline);
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    let id = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/net/speedtest/upload" => {
                            let _ = bridge::take_body(job.id, id);
                            reply(job.id, id, json!({"node":"backend"}), 200);
                        }
                        "/file/upload/init" => {
                            assert_eq!(event["body"]["value"]["quick_share"], true);
                            reply(
                                job.id,
                                id,
                                json!({"upload_id":"adaptive","missing":[0]}),
                                200,
                            );
                        }
                        "/file/upload/chunk" => {
                            assert_eq!(bridge::take_body(job.id, id).unwrap(), b"abcdefghij");
                            chunk = true;
                            reply(job.id, id, json!({}), 200);
                        }
                        "/file/upload/complete" => reply(
                            job.id,
                            id,
                            json!({"share_code":"test","temp_name":"temp","file_name":"file","file_size":10,"expire_time":"later","url_path":"/share/test"}),
                            200,
                        ),
                        path => panic!("unexpected {path}"),
                    }
                }
                "probe_share" => {
                    probe = true;
                    assert_eq!(event["url"], "http://unit.invalid/share/test");
                    reply(job.id, event["id"].as_u64().unwrap(), Value::Null, 200);
                }
                "done" => {
                    assert!(chunk && probe);
                    assert_eq!(event["data"]["entry_url"], "http://unit.invalid");
                    return;
                }
                "failed" => panic!("{event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}

#[test]
fn adaptive_upload_recovers_when_every_link_temporarily_fails() {
    let file = std::env::temp_dir().join(format!("sys-core-recovery-{}", uuid::Uuid::new_v4()));
    std::fs::write(&file, b"abcdefghij").unwrap();
    let input = serde_json::from_value(json!({
        "file": file,
        "remote_dir": "/test",
        "base_url": "http://unit.invalid",
        "main_node": "active",
        "nodes": [{"id":"active","url":"http://unit.invalid"}]
    }))
    .unwrap();
    let job = Job {
        id: bridge::start(input).unwrap(),
        file,
    };
    let deadline = Instant::now() + Duration::from_secs(10);
    let mut probes = 0;
    let mut chunks = 0;
    let mut abandons = 0;
    let mut completions = 0;
    loop {
        assert!(Instant::now() < deadline, "recovery did not complete");
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    let id = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/file/upload/init" => reply(
                            job.id,
                            id,
                            json!({"upload_id":"recovery","missing":[0]}),
                            200,
                        ),
                        "/net/speedtest/upload" => {
                            probes += 1;
                            let _ = bridge::take_body(job.id, id);
                            reply(job.id, id, json!({"node":"backend"}), 200);
                        }
                        "/file/upload/chunk" => {
                            chunks += 1;
                            assert_eq!(bridge::take_body(job.id, id).unwrap(), b"abcdefghij");
                            if chunks == 1 {
                                bridge::reply(job.id, id, Err("all links offline".into())).unwrap();
                            } else {
                                reply(job.id, id, json!({}), 200);
                            }
                        }
                        "/file/upload/abandon" => {
                            abandons += 1;
                            if abandons == 1 {
                                bridge::reply(job.id, id, Err("still offline".into())).unwrap();
                            } else {
                                reply(job.id, id, json!({}), 200);
                            }
                        }
                        "/file/upload/complete" => {
                            completions += 1;
                            if completions == 1 {
                                bridge::reply(job.id, id, Err("offline at completion".into()))
                                    .unwrap();
                            } else {
                                reply(
                                    job.id,
                                    id,
                                    json!({"file_name":"file","storage_path":"/test/file","file_size":10}),
                                    200,
                                );
                            }
                        }
                        path => panic!("unexpected {path}"),
                    }
                }
                "done" => {
                    assert_eq!(chunks, 2);
                    assert_eq!(abandons, 2);
                    assert_eq!(completions, 2);
                    assert!(probes >= 4, "recovery must reprobe the restored link");
                    assert_eq!(event["data"]["file_size"], 10);
                    return;
                }
                "failed" => panic!("temporary outage must not fail the upload: {event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(2));
    }
}

fn ffi_poll(id: u64) -> Value {
    let raw = sys_core::upload::fc_upload_poll(id);
    assert!(!raw.is_null());
    let text = unsafe { std::ffi::CStr::from_ptr(raw) }
        .to_str()
        .unwrap()
        .to_string();
    sys_core::fc_free_string(raw);
    serde_json::from_str(&text).unwrap()
}

fn ffi_reply(id: u64, request: u64, response: Value) {
    let result = std::ffi::CString::new(json!({"response": response}).to_string()).unwrap();
    assert_eq!(
        sys_core::upload::fc_upload_reply(id, request, result.as_ptr()),
        0
    );
}

#[test]
fn c_abi_bridge_pumps_requests_and_binary_bodies() {
    let file = std::env::temp_dir().join(format!("sys-core-c-abi-{}", uuid::Uuid::new_v4()));
    std::fs::write(&file, b"abcdefghij").unwrap();
    let input = std::ffi::CString::new(
        json!({
            "file": file,
            "remote_dir": "/test",
            "base_url": "http://unit.invalid",
            "chunk_size": 4,
            "concurrency": 2
        })
        .to_string(),
    )
    .unwrap();
    assert_eq!(sys_core::upload::fc_upload_bridge_version(), 1);
    let id = sys_core::upload::fc_upload_start(input.as_ptr());
    assert_ne!(id, 0);
    let deadline = Instant::now() + Duration::from_secs(5);
    let mut chunks = 0;
    let result = 'poll: loop {
        assert!(Instant::now() < deadline);
        for event in ffi_poll(id).as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    let request = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/file/upload/init" => ffi_reply(
                            id,
                            request,
                            json!({"code":200,"message":"","data":{"upload_id":"c-abi","missing":[0,1,2]}}),
                        ),
                        "/file/upload/chunk" => {
                            let index = event["query"]
                                .as_array()
                                .unwrap()
                                .iter()
                                .find(|p| p[0] == "index")
                                .unwrap()[1]
                                .as_str()
                                .unwrap()
                                .parse::<usize>()
                                .unwrap();
                            let mut data = std::ptr::null_mut();
                            let mut len = 0;
                            assert_eq!(
                                sys_core::upload::fc_upload_take_body(
                                    id, request, &mut data, &mut len,
                                ),
                                0
                            );
                            let body = unsafe { std::slice::from_raw_parts(data, len) };
                            assert_eq!(body, &b"abcdefghij"[index * 4..(index * 4 + 4).min(10)]);
                            sys_core::upload::fc_upload_body_free(data, len);
                            chunks += 1;
                            ffi_reply(id, request, json!({"code":200,"message":"","data":{}}));
                        }
                        "/file/upload/complete" => ffi_reply(
                            id,
                            request,
                            json!({"code":200,"message":"","data":{"file_name":"file","storage_path":"/test/file","file_size":10}}),
                        ),
                        path => panic!("unexpected {path}"),
                    }
                }
                "done" => break 'poll Ok(()),
                "failed" => break 'poll Err(event.clone()),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    };
    sys_core::upload::fc_upload_cancel(id);
    std::fs::remove_file(file).unwrap();
    assert_eq!(chunks, 3);
    result.unwrap();
}

#[test]
fn adaptive_final_bad_chunk_reinitializes_and_retries() {
    let file = std::env::temp_dir().join(format!("sys-core-bad-chunk-{}", uuid::Uuid::new_v4()));
    std::fs::write(&file, b"abcdefghij").unwrap();
    let input = serde_json::from_value(json!({
        "file": file,
        "remote_dir": "/test",
        "base_url": "http://unit.invalid",
        "main_node": "active",
        "nodes": [{"id":"active","url":"http://unit.invalid"}]
    }))
    .unwrap();
    let job = Job {
        id: bridge::start(input).unwrap(),
        file,
    };
    let deadline = Instant::now() + Duration::from_secs(5);
    let mut inits = 0;
    let mut chunks = 0;
    let mut completions = 0;
    loop {
        assert!(Instant::now() < deadline);
        for event in bridge::poll(job.id).unwrap().as_array().unwrap() {
            match event["type"].as_str().unwrap() {
                "request" => {
                    let id = event["id"].as_u64().unwrap();
                    match event["path"].as_str().unwrap() {
                        "/net/speedtest/upload" => {
                            let _ = bridge::take_body(job.id, id);
                            reply(job.id, id, json!({"node":"backend"}), 200);
                        }
                        "/file/upload/init" => {
                            inits += 1;
                            reply(
                                job.id,
                                id,
                                json!({"upload_id":format!("session-{inits}"),"missing":[0]}),
                                200,
                            );
                        }
                        "/file/upload/chunk" => {
                            chunks += 1;
                            assert_eq!(bridge::take_body(job.id, id).unwrap(), b"abcdefghij");
                            reply(job.id, id, json!({}), 200);
                        }
                        "/file/upload/complete" => {
                            completions += 1;
                            if completions == 1 {
                                reply(job.id, id, json!({"bad_index":0}), 422);
                            } else {
                                reply(
                                    job.id,
                                    id,
                                    json!({"file_name":"file","storage_path":"/test/file","file_size":10}),
                                    200,
                                );
                            }
                        }
                        path => panic!("unexpected {path}"),
                    }
                }
                "done" => {
                    assert_eq!(inits, 2);
                    assert_eq!(chunks, 2);
                    assert_eq!(completions, 2);
                    return;
                }
                "failed" => panic!("bad chunk must be retried: {event}"),
                _ => {}
            }
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}
