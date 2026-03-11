package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Usage: gontainer run <cmd> <args>
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: gontainer run <cmd> [args...]\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		child()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// run sets up namespaces and re-execs itself as "child"
func run() {
	fmt.Printf("Running %v as PID %d\n", os.Args[2:], os.Getpid())

	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}

	initCgroup()
	if err := cmd.Run(); err != nil {
		handleError(err)
	}
}

// child runs inside the new namespaces
func child() {
	fmt.Printf("Running %v as PID %d\n", os.Args[2:], os.Getpid())

	if err := syscall.Sethostname([]byte("gontainer")); err != nil {
		handleError(err)
	}

	if err := syscall.Chroot("/rootfs"); err != nil {
		handleError(err)
	}
	if err := syscall.Chdir("/"); err != nil {
		handleError(err)
	}

	defer func() {
		if err := syscall.Unmount("/proc", 0); err != nil {
			handleError(err)
		}
	}()

	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		handleError(err)
	}

	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		handleError(err)
	}
}

func handleError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func initCgroup() {
	cgroupPath := "/sys/fs/cgroup/gontainer"
	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		handleError(err)
	}
	// 256 MB memories
	if err := os.WriteFile(cgroupPath+"/memory.max", []byte("268435456"), 0o644); err != nil {
		handleError(err)
	}
	// Max 20 processes
	if err := os.WriteFile(cgroupPath+"/pids.max", []byte("20"), 0o644); err != nil {
		handleError(err)
	}
	if err := os.WriteFile(cgroupPath+"/cgroup.procs", []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		handleError(err)
	}
}
