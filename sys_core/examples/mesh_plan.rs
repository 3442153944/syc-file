//! JSON-lines adapter for exercising the exact shared planner against real test tunnels.
use std::io::{self, BufRead};

fn main() {
    for line in io::stdin().lock().lines() {
        let line = line.expect("read planner input");
        let input: sys_core::mesh::Input = serde_json::from_str(&line).expect("valid mesh input");
        println!(
            "{}",
            serde_json::to_string(&sys_core::mesh::plan(&input)).unwrap()
        );
    }
}
