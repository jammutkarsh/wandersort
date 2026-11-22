package exiftool

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

const leashHelperEnv = "WANDERSORT_LEASH_HELPER_EXIFTOOL"

// TestLeashHelper is the process TestExiftoolEndsWithAKilledProcess kills: it
// starts one exiftool, prints its PID and waits.
func TestLeashHelper(t *testing.T) {
	path := os.Getenv(leashHelperEnv)
	if path == "" {
		t.Skip("run by TestExiftoolEndsWithAKilledProcess")
	}
	e, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(e.cmd.Process.Pid)
	time.Sleep(time.Minute)
	e.Close()
}

// A process that is killed can't stop its exiftool, which polls its input
// forever once nothing writes to it: the leash must end it.
func TestExiftoolEndsWithAKilledProcess(t *testing.T) {
	helper := exec.Command(os.Args[0], "-test.run=^TestLeashHelper$")
	helper.Env = append(os.Environ(), leashHelperEnv+"="+installtest.Exiftool(t))
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer helper.Process.Kill()
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() {
		t.Fatalf("the helper printed no PID: %v", lines.Err())
	}
	pid, err := strconv.Atoi(lines.Text())
	if err != nil {
		t.Fatalf("the helper printed %q, want a PID", lines.Text())
	}
	if !alive(pid) {
		t.Fatal("exiftool is not running before its process is killed")
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helper.Wait()
	for deadline := time.Now().Add(10 * time.Second); alive(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
			t.Fatal("exiftool outlived the killed process that started it")
		}
	}
}

// alive reports whether a process with this PID exists.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false // Windows opens the process to find it
	}
	defer p.Release()
	return runtime.GOOS == "windows" || p.Signal(syscall.Signal(0)) == nil
}
