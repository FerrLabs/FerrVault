use std::fs::{self, File, OpenOptions};
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow};

const LOCK_FILE: &str = "session.lock";

pub struct ProcessLock {
    file: File,
}

impl ProcessLock {
    pub fn acquire(path: &Path) -> Result<Self> {
        if let Some(dir) = path.parent() {
            fs::create_dir_all(dir)
                .with_context(|| format!("creating lock directory {}", dir.display()))?;
        }
        let file = OpenOptions::new()
            .create(true)
            .truncate(false)
            .write(true)
            .open(path)
            .with_context(|| format!("opening lock file {}", path.display()))?;
        file.lock().context("waiting for the session lock")?;
        Ok(Self { file })
    }
}

impl Drop for ProcessLock {
    fn drop(&mut self) {
        let _ = self.file.unlock();
    }
}

pub fn default_lock_path() -> Result<PathBuf> {
    let state_home = std::env::var_os("XDG_STATE_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".local/state")))
        .or_else(|| std::env::var_os("LOCALAPPDATA").map(PathBuf::from))
        .ok_or_else(|| anyhow!("no state directory found for the session lock"))?;
    Ok(state_home.join("ferrvault").join(LOCK_FILE))
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use super::*;

    #[test]
    fn only_one_holder_is_inside_the_critical_section_at_a_time() {
        let dir = tempfile::tempdir().unwrap();
        let path = Arc::new(dir.path().join("nested/session.lock"));
        let inside = Arc::new(AtomicUsize::new(0));
        let overlaps = Arc::new(AtomicUsize::new(0));
        let threads: Vec<_> = (0..4)
            .map(|_| {
                let (path, inside, overlaps) = (path.clone(), inside.clone(), overlaps.clone());
                std::thread::spawn(move || {
                    for _ in 0..5 {
                        let _guard = ProcessLock::acquire(&path).unwrap();
                        if inside.fetch_add(1, Ordering::SeqCst) != 0 {
                            overlaps.fetch_add(1, Ordering::SeqCst);
                        }
                        std::thread::sleep(std::time::Duration::from_millis(2));
                        inside.fetch_sub(1, Ordering::SeqCst);
                    }
                })
            })
            .collect();
        for t in threads {
            t.join().unwrap();
        }
        assert_eq!(overlaps.load(Ordering::SeqCst), 0);
    }

    #[test]
    fn lock_is_released_on_drop() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("session.lock");
        drop(ProcessLock::acquire(&path).unwrap());
        drop(ProcessLock::acquire(&path).unwrap());
    }
}
