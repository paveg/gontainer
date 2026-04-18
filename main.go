// gontainer is a minimal container runtime that demonstrates how Docker works
// under the hood using Linux kernel features: namespaces, chroot, and cgroups.
//
// It implements process isolation similar to "docker run" by combining:
//   - Linux namespaces for visibility isolation (what a process can see)
//   - chroot for filesystem isolation (what files a process can access)
//   - cgroups v2 for resource limits (how much a process can consume)
//
// Architecture:
//
//	gontainer run /bin/sh
//	  │
//	  ├─ run()        [host context]
//	  │   ├─ Set up cgroups (memory/pids limits)
//	  │   ├─ Re-exec itself as "child" with new namespaces
//	  │   │   (CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS)
//	  │   │
//	  │   └─ child()  [isolated context — new namespaces active]
//	  │       ├─ Set hostname (UTS namespace)
//	  │       ├─ chroot into rootfs (filesystem isolation)
//	  │       ├─ Mount /proc (PID namespace visibility)
//	  │       └─ exec user command (/bin/sh)
package main

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: gontainer run <cmd> [args...]\n")
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		// "child" is an internal command, not meant to be called directly.
		// It is invoked by run() via /proc/self/exe to execute inside new namespaces.
		child()
	default:
		log.Fatalf("Unknown command: %s\n", os.Args[1])
	}
}

// run is the entry point for "gontainer run <cmd>".
// It configures cgroup resource limits and then re-executes the current binary
// as a child process with new Linux namespaces.
//
// Why re-exec instead of directly forking?
// Because CLONE_NEWPID only takes effect for *child* processes.
// The calling process itself does not get PID 1 — its child does.
// By re-executing via /proc/self/exe (a symlink to the current binary),
// the child() function runs as PID 1 inside the new PID namespace.
func run() {
	log.Printf("Running %v as PID %d\n", os.Args[2:], os.Getpid())

	// Re-exec ourselves with "child" as the first argument.
	// /proc/self/exe is a symlink to the currently running binary,
	// so this effectively calls: gontainer child <original args...>
	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// CLONE_NEWUTS: isolate hostname — changes to hostname inside the container
		//   don't affect the host. This is how "docker run --hostname" works.
		//
		// CLONE_NEWPID: isolate process IDs — the child sees itself as PID 1
		//   and cannot see host processes. This is why "ps" inside Docker
		//   only shows container processes.
		//
		// CLONE_NEWNS: isolate mount points — mounts inside the container
		//   (like /proc) don't propagate to the host. Without this,
		//   mounting /proc would overwrite the host's /proc.
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}

	// Set up cgroup resource limits before starting the child.
	// The child inherits the parent's cgroup, so limits apply automatically.
	cgroupPath := setupCgroup()

	// Clean up the cgroup after the container exits.
	// We must move ourselves out of the cgroup first — a cgroup directory
	// cannot be removed while any process is still in it (EBUSY).
	// Then rmdir (os.Remove) removes the directory; the kernel cleans up pseudo-files.
	defer func() {
		// Move ourselves back to the init cgroup (created by "make setup-cgroup")
		if err := os.WriteFile("/sys/fs/cgroup/init/cgroup.procs", []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			log.Fatal(err)
		}
		if err := os.Remove(cgroupPath); err != nil {
			log.Fatal(err)
		}
	}()

	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

// child runs inside the new namespaces created by run().
// At this point, the process already has:
//   - Its own UTS namespace (hostname is isolated)
//   - Its own PID namespace (this process is PID 1)
//   - Its own mount namespace (mounts won't affect host)
//
// This function completes the isolation by:
//  1. Setting a container hostname
//  2. Changing the root filesystem (chroot)
//  3. Mounting a fresh /proc for the new PID namespace
//  4. Executing the user's command
func child() {
	log.Printf("Running %v as PID %d\n", os.Args[2:], os.Getpid())

	// Set the container's hostname. Because we're in a new UTS namespace,
	// this only affects the container — the host hostname is unchanged.
	// This is equivalent to: docker run --hostname gontainer
	if err := syscall.Sethostname([]byte("gontainer")); err != nil {
		log.Fatal(err)
	}

	mergedPath := setupOverlayFS()

	// Change the root filesystem to an Alpine Linux minimal rootfs.
	// After chroot, "/" points to /rootfs on the host, so the container
	// cannot see or access any host files outside of /rootfs.
	// This is how Docker images work — each image provides a rootfs
	// that becomes the container's filesystem.
	//
	// Note: Docker uses pivot_root (more secure) instead of chroot,
	// but chroot demonstrates the same concept more simply.
	if err := syscall.Chroot(mergedPath); err != nil {
		log.Fatal(err)
	}
	os.MkdirAll("/dev", 0o755)
	syscall.Mknod("/dev/null", syscall.S_IFCHR|0o666, 1*256+3)
	// Must chdir after chroot, otherwise the process retains a reference
	// to the old root and could escape the chroot via relative paths.
	if err := syscall.Chdir("/"); err != nil {
		log.Fatal(err)
	}

	// Mount a new /proc filesystem for this PID namespace.
	// Without this, /proc still shows the host's process information
	// even though we're in a new PID namespace. After mounting,
	// "ps aux" will only show processes inside the container.
	//
	// Unmount on exit to avoid leaving stale mounts.
	defer func() {
		if err := syscall.Unmount("/proc", 0); err != nil {
			log.Fatal(err)
		}
	}()
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		log.Fatal(err)
	}

	// Execute the user's command (e.g., /bin/sh) inside the fully isolated environment.
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupCgroup configures cgroup v2 resource limits for the container.
//
// cgroups (control groups) are the Linux kernel mechanism for limiting
// resource usage. Docker uses them to implement flags like:
//   - "docker run --memory 256m"  → writes to memory.max
//   - "docker run --pids-limit 20" → writes to pids.max
//
// How it works:
//  1. Create a directory under /sys/fs/cgroup — this is a new cgroup
//  2. Write resource limits to files in that directory
//  3. Write a PID to cgroup.procs — that process (and its children) are now limited
//
// Prerequisites:
//
//	The cgroup subtree_control must have "+cpu +memory +pids" enabled.
//	See "make setup-cgroup" for the one-time setup.
func setupCgroup() string {
	cgroupPath := "/sys/fs/cgroup/gontainer"
	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		log.Fatal(err)
	}

	// Limit container memory to 256MB.
	// If the container exceeds this, the kernel's OOM killer terminates it.
	if err := os.WriteFile(cgroupPath+"/memory.max", []byte("268435456"), 0o644); err != nil {
		log.Fatal(err)
	}

	// Limit container to 0.5 CPU (equivalent to: docker run --cpus 0.5).
	// Format: "<quota> <period>" in microseconds.
	// "50000 100000" means the container can use 50ms out of every 100ms period.
	// Unlike memory/pids limits which cause hard failures (OOM kill / EAGAIN),
	// CPU limits throttle the process — it simply runs slower.
	if err := os.WriteFile(cgroupPath+"/cpu.max", []byte("50000 100000"), 0o644); err != nil {
		log.Fatal(err)
	}

	// Limit container to 20 processes.
	// Prevents fork bombs from consuming all system resources.
	// When the limit is reached, fork() returns EAGAIN ("Resource temporarily unavailable").
	if err := os.WriteFile(cgroupPath+"/pids.max", []byte("20"), 0o644); err != nil {
		log.Fatal(err)
	}

	// Add the current process to this cgroup.
	// Child processes (the container) inherit the parent's cgroup,
	// so the resource limits will apply to everything inside the container.
	if err := os.WriteFile(cgroupPath+"/cgroup.procs", []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		log.Fatal(err)
	}

	return cgroupPath
}

func setupOverlayFS() string {
	if err := os.MkdirAll("/overlay", 0o755); err != nil {
		log.Fatal(err)
	}

	if err := syscall.Mount("tmpfs", "/overlay", "tmpfs", 0, ""); err != nil {
		log.Fatal(err)
	}

	dirs := []string{"/overlay/upper", "/overlay/work", "/overlay/merged"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatal(err)
		}
	}

	opts := "lowerdir=/rootfs,upperdir=/overlay/upper,workdir=/overlay/work"

	if err := syscall.Mount("overlay", "/overlay/merged", "overlay", 0, opts); err != nil {
		log.Fatal(err)
	}

	return "/overlay/merged"
}
