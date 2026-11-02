package osx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// kinfoBytes builds a struct kinfo_proc as macOS returns it to a 64-bit process. The offsets are
// written here as literals (those of golang.org/x/sys/unix.KinfoProc), not taken from macproc.go,
// and every other byte is filled so a field read at a wrong offset shows.
func kinfoBytes(pid, ppid, pgid int32, stat byte, sec int64, usec int32, comm string) []byte {
	b := bytes.Repeat([]byte{0xA5}, 648)
	le := binary.LittleEndian
	le.PutUint64(b[0:], uint64(sec))
	le.PutUint32(b[8:], uint32(usec))
	b[36] = stat
	le.PutUint32(b[40:], uint32(pid))
	b[243+copy(b[243:243+16], comm)] = 0
	le.PutUint32(b[560:], uint32(ppid))
	le.PutUint32(b[564:], uint32(pgid))
	return b
}

const (
	sRun   = 2 // SRUN
	sSleep = 3 // SSLEEP
	sZomb  = 5 // SZOMB
)

func TestMacProcOf(t *testing.T) {
	for _, c := range []struct {
		name  string
		pid   int
		reply []byte
		want  macProc
		err   error // ErrNotRunning, or nil
		fails bool  // another error
	}{
		{name: "running", pid: 4242, reply: kinfoBytes(4242, 1, 4242, sRun, 1793616000, 123456, "holocron"),
			want: macProc{pid: 4242, ppid: 1, pgid: 4242, start: "1793616000.123456", comm: "holocron"}},
		{name: "sleeping, in its parent's group", pid: 77, reply: kinfoBytes(77, 70, 70, sSleep, 1700000000, 5, "ffmpeg"),
			want: macProc{pid: 77, ppid: 70, pgid: 70, start: "1700000000.000005", comm: "ffmpeg"}},
		{name: "start on a whole second", pid: 9, reply: kinfoBytes(9, 1, 9, sSleep, 1700000000, 0, "mediamtx"),
			want: macProc{pid: 9, ppid: 1, pgid: 9, start: "1700000000.000000", comm: "mediamtx"}},
		{name: "last microsecond of a second", pid: 9, reply: kinfoBytes(9, 1, 9, sSleep, 1, 999999, "a"),
			want: macProc{pid: 9, ppid: 1, pgid: 9, start: "1.999999", comm: "a"}},
		{name: "name of the full 16 bytes", pid: 5, reply: kinfoBytes(5, 1, 5, sRun, 1700000000, 1, "abcdefghijklmnop"),
			want: macProc{pid: 5, ppid: 1, pgid: 5, start: "1700000000.000001", comm: "abcdefghijklmnop"}},
		{name: "zombie", pid: 4242, reply: kinfoBytes(4242, 1, 4242, sZomb, 1793616000, 123456, "holocron"), err: ErrNotRunning},
		{name: "vanished: empty reply", pid: 4242, reply: nil, err: ErrNotRunning},
		{name: "short reply", pid: 4242, reply: kinfoBytes(4242, 1, 4242, sRun, 1, 1, "x")[:600], fails: true},
		{name: "another pid answered", pid: 4242, reply: kinfoBytes(4243, 1, 4243, sRun, 1, 1, "x"), fails: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := macProcOf(c.pid, c.reply)
			switch {
			case c.fails:
				if err == nil || errors.Is(err, ErrNotRunning) {
					t.Fatalf("err %v, want an error other than ErrNotRunning", err)
				}
			case c.err != nil:
				if !errors.Is(err, c.err) {
					t.Fatalf("err %v, want %v", err, c.err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if got != c.want {
					t.Fatalf("got %+v, want %+v", got, c.want)
				}
			}
		})
	}
}

// The token is a pure function of the start time the kernel stored at fork: the same process read
// twice gives the same token, a process started a microsecond later another one.
func TestMacStartTokenIdentity(t *testing.T) {
	a, _ := macProcOf(10, kinfoBytes(10, 1, 10, sRun, 1700000000, 42, "x"))
	b, _ := macProcOf(10, kinfoBytes(10, 1, 10, sSleep, 1700000000, 42, "x"))
	c, _ := macProcOf(10, kinfoBytes(10, 1, 10, sRun, 1700000000, 43, "x"))
	if a.start != b.start || a.start == c.start {
		t.Errorf("tokens %q %q %q", a.start, b.start, c.start)
	}
}

// procArgs2 builds a kern.procargs2 reply: argc, the exec path, NUL padding, argv, an environment.
func procArgs2(execPath string, argv ...string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, int32(len(argv)))
	b.WriteString(execPath)
	b.Write([]byte{0, 0, 0, 0}) // the terminator and alignment padding
	for _, a := range argv {
		b.WriteString(a)
		b.WriteByte(0)
	}
	b.WriteString("HOME=/Users/op\x00")
	return b.Bytes()
}

func TestExecPathOf(t *testing.T) {
	for _, c := range []struct {
		name, want string
		reply      []byte
	}{
		{"absolute", "/usr/local/bin/ffmpeg", procArgs2("/usr/local/bin/ffmpeg", "ffmpeg", "-i", "rtsp://cam")},
		{"spaces in the path", "/Applications/Holo Cron.app/Contents/MacOS/holocron", procArgs2("/Applications/Holo Cron.app/Contents/MacOS/holocron", "holocron", "up")},
		{"argv[0] differs from the path", "/opt/homebrew/bin/mediamtx", procArgs2("/opt/homebrew/bin/mediamtx", "mediamtx")},
		{"started by a relative path", "./holocron", procArgs2("./holocron", "./holocron", "host")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := execPathOf(c.reply)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
	for _, c := range []struct {
		name  string
		reply []byte
	}{
		{"empty", nil},
		{"argc only", []byte{1, 0, 0}},
		{"empty path", append([]byte{1, 0, 0, 0, 0}, "argv0\x00"...)},
		{"path without its NUL", append([]byte{1, 0, 0, 0}, "/bin/sl"...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, err := execPathOf(c.reply); err == nil {
				t.Fatalf("got %q, want an error", got)
			}
		})
	}
}

func TestMacProcEntries(t *testing.T) {
	var reply []byte
	reply = append(reply, kinfoBytes(1, 0, 1, sSleep, 1700000000, 1, "launchd")...)
	reply = append(reply, kinfoBytes(500, 1, 500, sZomb, 1700000100, 2, "ffmpeg")...)
	reply = append(reply, kinfoBytes(501, 1, 501, sRun, 1700000200, 3, "ffmpeg")...)
	got, err := macProcEntries(reply)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProcEntry{{Pid: 1, PPid: 0, Name: "launchd"}, {Pid: 501, PPid: 1, Name: "ffmpeg"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v (the zombie left out)", got, want)
	}
	if got, err := macProcEntries(nil); err != nil || len(got) != 0 {
		t.Errorf("empty reply: %v, %v", got, err)
	}
	if _, err := macProcEntries(reply[:len(reply)-1]); err == nil {
		t.Error("a reply that is not a multiple of kinfo_proc must fail")
	}
}
