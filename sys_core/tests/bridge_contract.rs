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
