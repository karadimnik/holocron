//go:build linux || darwin

package osx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// startSleep starts a sleep of its own that leads its own process group; the test kills and reaps it.
func startSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on PATH")
	}
	c := exec.Command(path, "600")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Process.Kill(); c.Wait() })
	return c
}

// The real inspector on a process this test started: a stable start token, the image, the group;
// once killed and not yet reaped (a zombie) it is not running.
func TestOSInspectorRealProcess(t *testing.T) {
	c := startSleep(t)
	in := OSInspector{}
	a, err := in.Inspect(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	b, err := in.Inspect(c.Process.Pid)
	if err != nil || a != b || a.Start == "" {
		t.Fatalf("two reads: %+v, %+v, %v", a, b, err)
	}
	want := c.Path // macOS: the path handed to execve
	if runtime.GOOS == "linux" {
		want, _ = filepath.EvalSymlinks(c.Path) // Linux: /proc/<pid>/exe, links resolved
	}
	if a.Image != want || a.Group != c.Process.Pid {
		t.Fatalf("%+v, want image %s and group %d", a, want, c.Process.Pid)
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := in.Inspect(c.Process.Pid)
		if errors.Is(err, ErrNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("killed and unreaped, Inspect still says %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Stop refuses an entry whose start token differs, and kills the recorded group when it matches.
func TestOSInspectorStopVerifies(t *testing.T) {
	c := startSleep(t)
	in := OSInspector{}
	info, err := in.Inspect(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	e := ChildEntry{Pid: c.Process.Pid, Start: info.Start, Image: info.Image, Group: info.Group}
	stale := e
	stale.Start += "0"
	if err := in.Stop(stale); err == nil {
		t.Fatal("Stop killed an entry with another start token")
	}
	if _, err := in.Inspect(c.Process.Pid); err != nil {
		t.Fatalf("the refused entry's process is gone: %v", err)
	}
	if err := in.Stop(e); err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err == nil {
		t.Fatal("sleep exited cleanly; want killed")
	} else if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); !ok || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("exit %v, want SIGKILL", err)
	}
}

func TestSetOwnerOnlyUnix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "secret")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Current()
	if err := p.SetOwnerOnly(f); err != nil {
		t.Fatal(err)
	}
	if err := p.SetOwnerOnly(sub); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(sub); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
	if err := p.SetOwnerOnly(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing path must fail")
	}
}
