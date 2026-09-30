use std::{env, fs, process};

fn main() {
    let var = |k: &str| env::var(k).unwrap_or_default();
    println!("event={}", var("KICKD_EVENT"));
    println!("trigger={}", var("KICKD_TRIGGER"));
    println!("msg={}", var("KICKD_DATA_MSG"));
    println!("dir={}", env::current_dir().unwrap().display());
    println!("payload={}", fs::read_to_string(var("KICKD_PAYLOAD_FILE")).unwrap());
    eprintln!("to stderr");
    process::exit(var("KICKD_DATA_CODE").parse().unwrap_or(1));
}
