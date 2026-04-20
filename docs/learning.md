# gontainer 実装の学習メモ

自作コンテナランタイムを書きながら拾った、ドキュメントだけでは補いにくい
知見の local 保存場所です。広い Phase 1 学習 arc (OCI image spec、docker pull
フロー、OverlayFS 3原則など) は [career#5][issue5] に全体像があります。

ここに書くのは **この実装を将来読んだときに再構築が必要な知識** だけ ─
実装済みの事実は [main.go](../main.go) と [README.md](../README.md) で十分なので、
ここは「なぜその実装になったか / なぜカーネルがそう振る舞うか」を中心にします。

## 実装完了スコープ

| 範囲 | PR | 最終 commit |
|---|---|---|
| Namespace 分離 (UTS / PID / Mount) + chroot + cgroup v2 | 初期実装 | [8e372b2](https://github.com/paveg/gontainer/commit/8e372b2) 以前 |
| OverlayFS による書き込みレイヤー分離 | [PR #1][pr1] | [8e372b2](https://github.com/paveg/gontainer/commit/8e372b2) |
| ネットワーク一式 (netns + veth + bridge + NAT + PR_SET_PDEATHSIG) | [PR #2][pr2] | [4d5290a](https://github.com/paveg/gontainer/commit/4d5290a) |

## カーネル境界の挙動 — 実装中に確認した事実

### `CLONE_NEWNET` と `clone(2)` は原子的

- clone(2) のフラグなので、**子プロセスの生成と新 netns への所属は同時**に発生する
- `cmd.Start()` が return した時点で child は既に新 netns にいる
- つまり親が `ip link set vethc<pid> netns <pid>` を呼ぶ時点で netns は確実に存在する ─ race window なし
- この性質のおかげで親子間に明示的な "netns ready" シグナルを授受する必要がない

### `PR_SET_PDEATHSIG` は `execve(2)` を越えて保持される

- `prctl(PR_SET_PDEATHSIG, SIGKILL)` を child() 先頭で呼んでおくと、以降 execve しても設定が消えない (Linux 2.3.15+)
- 親 (run()) が SIGKILL されても child はカーネルから SIGKILL を受ける → 孤児化しない
- 例外: 途中で **setuid が効く** と pdeathsig はクリアされる (セキュリティ上の理由)。gontainer は uid 変更をしないので問題なし

### veth pair は片端の netns 消失で両端が自動削除される

- `ip link del` を呼ばなくても、片方の netns が消えるとカーネルがもう片方も消す
- PR_SET_PDEATHSIG と組み合わせれば、SIGKILL で親が死ぬ → 子も SIGKILL → 子の netns 破棄 → `vethh<pid>` も自動削除
- defer / signal handler による cleanup は「正常終了の美観」であって「リーク防止」ではない ─ SIGKILL でもリークしない

### cgroup v2 の "No Internal Process Constraint"

- プロセスが入っている cgroup には **子 cgroup を作って controller を割り当てることができない** (EBUSY)
- `subtree_control` に `+memory` を書こうとするとルート cgroup 内のプロセスが邪魔してエラーになる
- 回避策: `/sys/fs/cgroup/init/` を作ってルート直下のプロセスを全部そこに退避させる (→ [Makefile](../Makefile) の `setup-cgroup` ターゲット)
- これは毎コンテナ再起動後に必要 (kernel 上のメモリ状態なので永続化しない)

### `/sys/class/net` は mount 時の netns にタグ付けされる

- sysfs は mount 時の netns を覚える仕様
- 新 netns の中から `/sys/class/net` を見ると、その netns に属するインターフェースだけ見える ─ のが理想だが、**chroot 前に inherited な sysfs を読むと親 netns の見え方になる**
- gontainer では child() で `ip -o link show` を使って kernel に直接問い合わせる形にしている(sysfs 経由の `/sys/class/net/veth-cont` チェックだと新 netns の veth が見えないケースがあった)

## 3階層の入れ子実行環境

```
Mac (ARM64)
 └── Colima VM (Linux aarch64)
       └── gontainer-dev コンテナ (Docker, --privileged --cgroupns=private)  ← "ホスト" 役
             └── gontainer プロセス (自作 runtime)
                   └── child() (新 namespaces + overlay chroot)
```

- Mac から `10.0.0.0/24` に ping は届かない(各階層で NAT が必要)
- gontainer-dev コンテナが `--privileged` なのは、cgroup v2 subtree_control と iptables の書き込みを要求するため
- `--cgroupns=private` を付けることで、gontainer-dev 内部の `/sys/fs/cgroup` が独立した cgroup namespace になる ─ ここで自由に gontainer 用の cgroup tree を組み立てられる

## 実装でのハマりポイント

### OverlayFS (PR #1)

- **overlayfs-on-overlayfs は拒否される** → docker が既に overlay を使っている上で gontainer-dev 内に overlay を作れない
- **回避**: `/overlay` を tmpfs として mount してから、その中に `upperdir`/`workdir` を作る (tmpfs サンドイッチ)
- `upperdir` と `workdir` は `lowerdir` の外に置く必要がある(同じ FS ツリー内に入れると EINVAL)
- `/dev/null` 等のデバイスファイルは Alpine minirootfs に含まれない → `mknod(/dev/null, c 1 3)` で手動作成が必須
- アーキテクチャ(x86_64 / aarch64)は musl libc の動的リンク上、ホストと揃える必要がある

### ネットワーク (PR #2)

- veth pair の `ip link add` は **親 (run()) で実行**。その後 `ip link set vethc<pid> netns <pid>` で片端を子 netns に送る
- child 側は `/sys/class/net` ではなく `ip -o link show` で veth の出現を検知する(sysfs タグ問題)
- 両端に異なる IP を振る必要がある。同じサブネットの同じ IP にすると routing が壊れる
- `state UP` だけでなく `LOWER_UP` (ペア側も UP) を待たないと通信できないケースがある
- **複数コンテナ対応**: `vethh<pid>` / `vethc<pid>` のように PID 連番で命名、IP も `(pid % 253) + 2` で採番して衝突回避
- iptables ルールは `-C` で存在チェック → なければ `-A` の idempotent 適用
- `ip` コマンドの引数には CIDR 有無の使い分けがある:
  - `ip addr add 10.0.0.2/24 dev veth` ← /24 付き
  - `ip route add default via 10.0.0.1` ← /24 なし

### クリーンアップ

| 終了経路 | 発火するクリーンアップ |
|---|---|
| 正常 exit | `defer` で veth 削除 + cgroup rmdir |
| SIGINT / SIGTERM | `signal.Notify` → goroutine → veth 削除 → `os.Exit(130)` |
| SIGKILL で親死亡 | defer も signal handler も走らない。が `PR_SET_PDEATHSIG` で子も SIGKILL → netns 破棄 → kernel が veth 両端を自動削除 |
| panic / SIGSEGV | defer は走る可能性あり(Go runtime 次第)。最悪ケースでも SIGKILL 経路と同じく kernel が回収 |

**共有リソースは cleanup しない方針**:

- `br0` bridge ─ 複数コンテナで共有。最後のコンテナ終了時に削除する仕組みは未実装(コンテナ参照カウントの概念が要る)
- iptables MASQUERADE ルール ─ bridge と同様、共有インフラ扱い
- cgroup の空ディレクトリ残骸 ─ PID ベースで衝突しないため、sweep 不要と判断

## containerd との対比

詳細は [PR #2 の containerd 対比コメント](https://github.com/paveg/gontainer/pull/2) 参照。5つの本質的な違いと、gontainer 各部の containerd 対応箇所:

| gontainer の実装 | containerd での担当 | 備考 |
|---|---|---|
| `syscall.SysProcAttr.Cloneflags = CLONE_NEWUTS\|PID\|NS\|NET` | shim → runc create → `config.json` の `linuxNamespaces` | containerd 自体は namespace を直接は扱わず runc に委譲 |
| `syscall.Mount("overlay", ...)` in setupOverlayFS() | snapshotter.Prepare() | snapshotter が image layer と container rootfs を同じ抽象で扱う |
| `prctl(PR_SET_PDEATHSIG, SIGKILL)` | shim process が container lifecycle の supervisor | containerd 再起動でもコンテナが生存できる根拠 |
| `ip link add veth` + bridge + iptables | **CNI plugin** (containerd 外) | Flannel / Calico / Cilium が担当する領域 ─ containerd は netns pid を渡すだけ |
| cgroup v2 書き込み (memory.max 等) | runc の cgroups.Manager | `config.json` の `linuxResources` から翻訳 |

## Advanced — 意図的にスコープ外にした領域

「ここまでで十分」と線引きした理由は、これらがカーネル境界の理解ではなく
**境界の上に積む抽象層** の学習になるため。必要になったら掘る候補:

| 領域 | 学習トピック | 紐づく production runtime |
|---|---|---|
| User namespace | rootless containers の仕組み、UID/GID mapping | Podman / runc rootless mode |
| pivot_root | chroot 脱出攻撃を防ぐ root 切替 | runc |
| seccomp | syscall allowlist / denylist の per-container 適用 | Docker default profile / runc |
| AppArmor / SELinux | 強制アクセス制御プロファイル | Kubernetes PSP / PodSecurity |
| OCI image pull / registry | レイヤーの content-addressable fetch | containerd image store |
| CNI plugin | pluggable per-container networking | Cilium / Flannel / Calico |
| OCI runtime-spec + shim | `config.json` 準拠 + lifecycle supervisor | runc + containerd-shim |

## 参照

- [PR #1 — OverlayFS][pr1]
- [PR #2 — Container networking (netns + veth + bridge + NAT)][pr2]
- [career#5 — Phase 1 全体像][issue5]
- [Linux namespaces(7)](https://man7.org/linux/man-pages/man7/namespaces.7.html)
- [cgroups(7)](https://man7.org/linux/man-pages/man7/cgroups.7.html)
- [prctl(2) — PR_SET_PDEATHSIG](https://man7.org/linux/man-pages/man2/prctl.2.html)
- [OCI Runtime Spec](https://github.com/opencontainers/runtime-spec)
- [OCI Image Spec](https://github.com/opencontainers/image-spec)

[pr1]: https://github.com/paveg/gontainer/pull/1
[pr2]: https://github.com/paveg/gontainer/pull/2
[issue5]: https://github.com/paveg/career/issues/5
