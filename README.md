# gontainer

A minimal container runtime built from scratch in Go.

Learning project to understand what happens beneath `docker run` — namespaces, cgroups, filesystem isolation, and container networking.

## Goals

- Understand Linux container primitives by implementing them from scratch
- Build a working container runtime that can isolate and run processes
- Use this knowledge to contribute to real container runtimes (containerd/runc/youki)

## Roadmap

### Step 1: Process Isolation (Namespaces)
- [x] Fork/exec child process with `CLONE_NEWUTS`, `CLONE_NEWPID`, `CLONE_NEWNS`
- [ ] Set custom hostname inside container (`UTS namespace`)
- [ ] Verify PID 1 inside container (`PID namespace`)

### Step 2: Filesystem Isolation (chroot)
- [ ] Download and extract Alpine Linux minirootfs
- [ ] `chroot` into rootfs
- [ ] Mount `/proc` inside container

### Step 3: Resource Limits (cgroups v2)
- [ ] Memory limit (e.g. 100MB)
- [ ] CPU limit
- [ ] PID limit (fork bomb protection)
- [ ] Cleanup cgroups on container exit

### Step 4: Image Management
- [ ] Extract rootfs from tarball
- [ ] OverlayFS layer support (read-only base + writable upper)
- [ ] Simple `pull` command to fetch Alpine minirootfs

### Step 5: Networking
- [ ] Create veth pair
- [ ] Set up bridge interface
- [ ] NAT for outbound traffic
- [ ] Container-to-container communication

### Step 6: CLI & UX
- [ ] `gontainer run <image> <cmd>`
- [ ] `gontainer ps` (list running containers)
- [ ] `gontainer exec` (attach to running container)

## Architecture

```
gontainer run alpine /bin/sh
  │
  ├─ run(): create namespaces (UTS, PID, Mount)
  │    └─ re-exec as "child" via /proc/self/exe
  │
  └─ child(): inside new namespaces
       ├─ chroot into rootfs
       ├─ mount /proc
       ├─ set cgroups limits
       ├─ setup networking
       └─ exec user command (/bin/sh)
```

## References

- [Liz Rice - Containers From Scratch](https://github.com/lizrice/containers-from-scratch)
- [Build Your Own Container Runtime](https://medium.com/@ssttehrani/containers-from-scratch-with-golang-c29752c22a4a)
- [Linux Namespaces (man7.org)](https://man7.org/linux/man-pages/man7/namespaces.7.html)
- [cgroups v2 (kernel.org)](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)
- [OCI Runtime Spec](https://github.com/opencontainers/runtime-spec)
- [runc](https://github.com/opencontainers/runc)
- [containerd](https://github.com/containerd/containerd)

## Requirements

- Linux (namespaces/cgroups are Linux-only)
- Go 1.22+
- Root privileges (`sudo`)

## Usage

```bash
go build -o gontainer .
sudo ./gontainer run /bin/sh
```

## License

MIT
