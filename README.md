# gontainer

A minimal container runtime built from scratch in Go.

Learning project to understand what happens beneath `docker run` — namespaces, cgroups, and filesystem isolation.

## What Docker Actually Does

Docker containers are not virtual machines. They are regular Linux processes with **isolation** and **resource limits** applied using kernel features.

```mermaid
graph LR
    subgraph "docker run alpine /bin/sh"
        A[Regular Linux Process]
    end
    subgraph "Kernel Features Applied"
        B[Namespaces] -->|isolate| E[Process sees only its own world]
        C[chroot / pivot_root] -->|isolate| F[Process sees only its own files]
        D[cgroups] -->|limit| G[Process can only use X MB / N PIDs]
    end
    A --> B
    A --> C
    A --> D
```

## Architecture

```mermaid
sequenceDiagram
    participant User
    participant run as run()<br/>Host Context
    participant kernel as Linux Kernel
    participant child as child()<br/>Isolated Context

    User->>run: gontainer run /bin/sh
    run->>run: setupCgroup()<br/>cpu.max = 0.5 CPU<br/>memory.max = 256MB<br/>pids.max = 20

    run->>kernel: clone(CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS)
    kernel->>child: New process with isolated namespaces

    Note over child: PID = 1 (new PID namespace)

    child->>kernel: sethostname("gontainer")
    Note over child: Hostname isolated

    child->>kernel: chroot("/rootfs") + chdir("/")
    Note over child: Filesystem isolated (Alpine rootfs)

    child->>kernel: mount("proc", "/proc", "proc")
    Note over child: /proc shows only container processes

    child->>child: exec /bin/sh
    child-->>User: Interactive shell inside container
```

## Linux Kernel Features

```mermaid
graph TB
    subgraph ns ["Namespaces — What can the process see?"]
        UTS["UTS Namespace<br/>CLONE_NEWUTS<br/>Isolates hostname"]
        PID["PID Namespace<br/>CLONE_NEWPID<br/>Isolates process tree"]
        MNT["Mount Namespace<br/>CLONE_NEWNS<br/>Isolates mount points"]
    end

    subgraph fs ["Filesystem — What files can the process access?"]
        CHR["chroot<br/>Changes root directory<br/>to Alpine rootfs"]
        PROC["/proc mount<br/>Fresh procfs for<br/>new PID namespace"]
    end

    subgraph cg ["cgroups v2 — How much can the process use?"]
        CPU["cpu.max<br/>0.5 CPU limit"]
        MEM["memory.max<br/>256 MB limit"]
        PIDS["pids.max<br/>20 process limit"]
    end

    style UTS fill:#4a9eff,color:#fff
    style PID fill:#4a9eff,color:#fff
    style MNT fill:#4a9eff,color:#fff
    style CHR fill:#ff9f43,color:#fff
    style PROC fill:#ff9f43,color:#fff
    style CPU fill:#ee5a24,color:#fff
    style MEM fill:#ee5a24,color:#fff
    style PIDS fill:#ee5a24,color:#fff
```

## Docker Feature Mapping

| Docker CLI | Linux Kernel | gontainer |
|---|---|---|
| `--hostname X` | UTS namespace + `sethostname()` | `CLONE_NEWUTS` + `syscall.Sethostname()` |
| Process isolation | PID namespace + procfs | `CLONE_NEWPID` + `mount("proc")` |
| Mount isolation | Mount namespace | `CLONE_NEWNS` |
| Docker image | `chroot` / `pivot_root` | `syscall.Chroot("/rootfs")` |
| `--memory 256m` | cgroup `memory.max` | `WriteFile("memory.max", "268435456")` |
| `--cpus 0.5` | cgroup `cpu.max` | `WriteFile("cpu.max", "50000 100000")` |
| `--pids-limit 20` | cgroup `pids.max` | `WriteFile("pids.max", "20")` |

## Roadmap

### Step 1: Process Isolation (Namespaces)
- [x] Fork/exec child process with `CLONE_NEWUTS`, `CLONE_NEWPID`, `CLONE_NEWNS`
- [x] Set custom hostname inside container (UTS namespace)
- [x] Verify PID 1 inside container (PID namespace)

### Step 2: Filesystem Isolation (chroot)
- [x] Download and extract Alpine Linux minirootfs
- [x] `chroot` into rootfs
- [x] Mount `/proc` inside container

### Step 3: Resource Limits (cgroups v2)
- [x] Memory limit (256MB)
- [x] PID limit (fork bomb protection)
- [x] CPU limit (0.5 CPU)
- [x] Cleanup cgroups on container exit

### Step 4: Image Management
- [ ] OverlayFS layer support (read-only base + writable upper)
- [ ] Simple `pull` command to fetch Alpine minirootfs

### Step 5: Networking
- [ ] Network namespace (`CLONE_NEWNET`)
- [ ] Create veth pair
- [ ] Set up bridge interface
- [ ] NAT for outbound traffic

### What gontainer Does NOT Implement

| Feature | What it does |
|---|---|
| **Network namespace** | Isolates network stack (own IP, ports, routing) |
| **User namespace** | Maps UID/GID (root inside, unprivileged outside) |
| **pivot_root** | More secure alternative to chroot |
| **OverlayFS** | Copy-on-write image layers |
| **seccomp** | Syscall filtering |
| **AppArmor / SELinux** | Mandatory access control |

## Prerequisites

- Docker (via colima, Docker Desktop, or similar)
- Go 1.25+

## Setup

```bash
# Start the development container
docker run --privileged --cgroupns=private -it -d \
  --name gontainer-dev \
  -v $(pwd):/app -w /app \
  golang:1.25 bash

# Download Alpine rootfs inside the container
docker exec gontainer-dev sh -c \
  'mkdir -p /rootfs && curl -sL https://dl-cdn.alpinelinux.org/alpine/v3.21/releases/aarch64/alpine-minirootfs-3.21.3-aarch64.tar.gz | tar xz -C /rootfs'

# Enable cgroup controllers (required once per container restart)
make setup-cgroup
```

## Usage

```bash
make build    # Build inside the container
make run      # Build and run (opens /bin/sh inside gontainer)
make shell    # Open a shell in the dev container
```

Inside gontainer:

```bash
hostname              # → gontainer (isolated)
ps aux                # → only container processes
ls /                  # → Alpine rootfs (not host)
cat /proc/self/cgroup # → /gontainer (cgroup applied)
```

## References

- [Liz Rice - Containers From Scratch (YouTube)](https://www.youtube.com/watch?v=8fi7uSYlOdc)
- [Linux namespaces(7)](https://man7.org/linux/man-pages/man7/namespaces.7.html)
- [cgroups(7)](https://man7.org/linux/man-pages/man7/cgroups.7.html)
- [chroot(2)](https://man7.org/linux/man-pages/man2/chroot.2.html)
- [OCI Runtime Spec](https://github.com/opencontainers/runtime-spec)

## License

MIT
