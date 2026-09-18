package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAcquireLockExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "session.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if unlock, err := AcquireLock(path); err == nil {
		unlock()
		t.Fatal("second session lock succeeded")
	}
	release()
	release() // Release must be idempotent.
	unlocked, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	unlocked()
}

func TestPrivateReadRejectsFIFO(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "pipe")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(path, 100); err == nil {
		t.Fatal("FIFO accepted as private data")
	}
}

func TestAcquireLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("TUIGRAM_LOCK_TEST_PATH"); path != "" {
		if release, err := AcquireLock(path); err == nil {
			release()
			os.Exit(2)
		}
		return
	}
	path := filepath.Join(privateTempDir(t), "session.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquireLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TUIGRAM_LOCK_TEST_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process ignored held lock: %v\n%s", err, out)
	}
}

func TestAcquireLockRejectsSymlinkAndPermissions(t *testing.T) {
	dir := privateTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if release, err := AcquireLock(link); err == nil {
		release()
		t.Fatal("symlink lock accepted")
	}
	if err := os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	if release, err := AcquireLock(target); err == nil {
		release()
		t.Fatal("permissive lock accepted")
	}
}
