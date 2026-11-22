package exiftool

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

const leashHelperEnv = "WANDERSORT_LEASH_HELPER_EXIFTOOL"

// TestLeashHelper is the process TestExiftoolEndsWithAKilledProcess kills: it
// starts one exiftool, prints the PIDs it runs as once it answers, and waits.
func TestLeashHelper(t *testing.T) {
	path := os.Getenv(leashHelperEnv)
	if path == "" {
		t.Skip("run by TestExiftoolEndsWithAKilledProcess")
	}
	e, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Extract(t.Context(), "no-such-file.jpg"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("Extract on a missing file = %v, want ErrUnreadable", err)
	}
	var pids []string
	for _, pid := range processTree(t, e.cmd.Process.Pid) {
		pids = append(pids, strconv.Itoa(pid))
	}
	fmt.Println(strings.Join(pids, " "))
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
		t.Fatalf("the helper printed no PIDs: %v", lines.Err())
	}
	var pids []int
	for _, field := range strings.Fields(lines.Text()) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("the helper printed %q, want PIDs", lines.Text())
		}
		if !alive(pid) {
			t.Fatalf("exiftool process %d is not running before its parent is killed", pid)
		}
		pids = append(pids, pid)
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helper.Wait()
	for deadline := time.Now().Add(10 * time.Second); slices.ContainsFunc(pids, alive); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			for _, pid := range pids {
				if p, err := os.FindProcess(pid); err == nil {
					p.Kill()
				}
			}
			t.Fatalf("exiftool (processes %v) outlived the killed process that started it", pids)
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
