use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

use reqwest::Url;

const LAUNCH_GRACE: Duration = Duration::from_secs(2);
const LAUNCH_POLL: Duration = Duration::from_millis(50);

pub fn browser_available() -> bool {
    cfg!(any(target_os = "macos", target_os = "windows"))
        || display_available(|key| std::env::var(key).ok())
}

pub fn display_available(env: impl Fn(&str) -> Option<String>) -> bool {
    ["DISPLAY", "WAYLAND_DISPLAY", "BROWSER"]
        .iter()
        .any(|key| env(key).is_some_and(|v| !v.trim().is_empty()))
}

pub fn open_in_browser(url: &Url) -> Result<(), String> {
    eprintln!("ferrvault: opening your browser to log in. If it does not open, visit:\n{url}");
    let mut command = if cfg!(target_os = "macos") {
        let mut c = Command::new("open");
        c.arg(url.as_str());
        c
    } else if cfg!(target_os = "windows") {
        let mut c = Command::new("rundll32");
        c.args(["url.dll,FileProtocolHandler", url.as_str()]);
        c
    } else {
        let mut c = Command::new("xdg-open");
        c.arg(url.as_str());
        c
    };
    launch(&mut command)
}

fn launch(command: &mut Command) -> Result<(), String> {
    let program = command.get_program().to_string_lossy().into_owned();
    let mut child = command
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| format!("{program}: {e}"))?;
    let deadline = Instant::now() + LAUNCH_GRACE;
    loop {
        match child.try_wait() {
            Ok(Some(status)) if status.success() => return Ok(()),
            Ok(Some(status)) => return Err(format!("{program} exited with {status}")),
            Ok(None) if Instant::now() < deadline => std::thread::sleep(LAUNCH_POLL),
            Ok(None) => return Ok(()),
            Err(e) => return Err(format!("{program}: {e}")),
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    fn env<'a>(pairs: &'a [(&'a str, &'a str)]) -> impl Fn(&str) -> Option<String> + 'a {
        move |key| {
            pairs
                .iter()
                .find(|(k, _)| *k == key)
                .map(|(_, v)| (*v).to_owned())
        }
    }

    #[test]
    fn any_display_or_browser_variable_counts() {
        assert!(display_available(env(&[("DISPLAY", ":0")])));
        assert!(display_available(env(&[("WAYLAND_DISPLAY", "wayland-0")])));
        assert!(display_available(env(&[("BROWSER", "firefox")])));
    }

    #[test]
    fn no_or_blank_variables_mean_no_browser() {
        assert!(!display_available(env(&[])));
        assert!(!display_available(env(&[
            ("DISPLAY", ""),
            ("BROWSER", "  ")
        ])));
    }

    #[test]
    fn a_launcher_exiting_non_zero_is_a_failure() {
        let err = launch(Command::new("sh").args(["-c", "exit 3"])).unwrap_err();
        assert!(err.contains("exited with"), "{err}");
    }

    #[test]
    fn a_launcher_exiting_zero_is_a_success() {
        assert!(launch(Command::new("sh").args(["-c", "exit 0"])).is_ok());
    }

    #[test]
    fn a_missing_launcher_is_a_failure() {
        assert!(launch(&mut Command::new("ferrvault-no-such-launcher")).is_err());
    }
}
