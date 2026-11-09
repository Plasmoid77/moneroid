# Installing Moneroid

The fast path is `install.sh` from the repository root (see README): it performs exactly steps 1–4 below with the newest upstream releases verified by their authors' signatures (spec INSTALL-06), idempotently, as a user with sudo. Below are the same steps by hand, as root unless noted; the Moneroid binary itself does none of this (spec INSTALL-01).

## Migrating from Monerizer (before v0.4.0)

The project was renamed; the unit, group, user and path names changed (`monerizer` → `moneroid`). Migration: stop the old installation (`monerizer stop`), then `sh install.sh --wallet …` (or the steps below), carry your edits from `/etc/monerizer/*` over to `/etc/moneroid/*` (rename the paths inside too), optionally move `/var/lib/monerizer/p2pool` → `/var/lib/moneroid/p2pool` (the P2Pool cache, speeds up the first sync; `chown -R moneroid-p2pool:moneroid`), and remove the old one: `systemctl disable --now monerizer-p2pool monerizer-xmrig`, `rm /etc/systemd/system/monerizer-*.service /etc/polkit-1/rules.d/50-monerizer.rules /usr/local/bin/monerizer`, `userdel monerizer-p2pool monerizer-xmrig`, `groupdel monerizer`.

## 1. Upstream binaries

```sh
# P2Pool: tar.gz + sha256sums.txt.asc, signed by SChernykh  1FCA AB4D 3DC3 310D 16CB D508 C47F 82B5 4DA8 7ADF (keys/p2pool-SChernykh.asc)
# XMRig:  tar.gz + SHA256SUMS + SHA256SUMS.sig, signed by XMRig  9AC4 CEA8 E66E 35A5 C7CD DC1B 446A 5363 8BE9 4409 (keys/xmrig.asc)
#   gpg --import keys/*.asc; gpg --verify sha256sums.txt.asc; gpg --verify SHA256SUMS.sig SHA256SUMS; then sha256sum -c
install -o root -g root -m 0755 p2pool /usr/local/bin/p2pool
install -o root -g root -m 0755 xmrig  /usr/local/bin/xmrig
```

Another location → edit `ExecStart=` in the unit files (not the Moneroid config).

## 2. Users, group, files

```sh
groupadd -r moneroid
useradd -r -g moneroid -s /usr/sbin/nologin -d /var/lib/moneroid/p2pool -M moneroid-p2pool
useradd -r -g moneroid -s /usr/sbin/nologin -d /var/lib/moneroid/xmrig  -M moneroid-xmrig
install -d -o root -g root -m 0755 /etc/moneroid
install -o root -g moneroid -m 0640 examples/p2pool.conf  /etc/moneroid/p2pool.conf
install -o root -g moneroid -m 0640 examples/xmrig.json   /etc/moneroid/xmrig.json
install -o root -g root      -m 0644 examples/moneroid.toml /etc/moneroid/moneroid.toml
install -o root -g root      -m 0644 examples/nodes.txt    /etc/moneroid/nodes.txt
install -o root -g root -m 0644 systemd/moneroid-p2pool.service systemd/moneroid-xmrig.service systemd/moneroid-guard.service /etc/systemd/system/
install -o root -g root -m 0755 moneroid /usr/local/bin/moneroid
systemctl daemon-reload
usermod -aG moneroid OPERATOR      # reads the Data API; log in again afterwards
```

The directories `/var/lib/moneroid/{p2pool,xmrig}` (0700) and `/run/moneroid-p2pool-api` (0750, group `moneroid`) are created by systemd when the services start; do not create them by hand.

## 3. Native configs

`/etc/moneroid/p2pool.conf` — the P2Pool params-file (`key = value`, booleans as `1`):
- `wallet` — the primary address (`4…`); P2Pool does not start without it.
- `host`, `rpc-port`, `zmq-port` — the Monero node. Either by hand or `sudo moneroid node select` (probes the candidates from `nodes.txt`).
- `mini = 1` (the example's default) / `nano = 1` / nothing for main.
- The remaining lines (`data-api`, `local-api`, `no-upnp`, `no-log-file`, `no-color`) are integration settings, no need to change them.

`/etc/moneroid/xmrig.json` — the native XMRig config. In the example: `autosave=false`, `watch=false` (otherwise XMRig tries to rewrite a root-owned file), `colors=false`, HTTP API on `127.0.0.1:18088` in restricted mode, `api.id = moneroid-xmrig`, pool `127.0.0.1:3333`. Threads — `cpu.max-threads-hint` (100 = all). The donation is the upstream `donate-level` setting; the example does not touch it.

API token (optional): set `http.access-token` in `xmrig.json` and the same text in `/etc/moneroid/xmrig-api.token` (`root:moneroid 0640`), uncomment `token_file` in `moneroid.toml`.

## 4. First start

```sh
moneroid doctor            # expected: warn UNIT_ENABLED (autostart not enabled), warn XMRIG_HUGEPAGES
moneroid start             # or sudo moneroid start; P2Pool syncs the sidechain in 2–5 minutes
moneroid status
moneroid logs --follow p2pool   # "SideChain SYNCHRONIZED", no repeated "ZMQReader disconnected"
systemctl enable moneroid-p2pool.service moneroid-xmrig.service   # autostart after boot
systemctl enable --now moneroid-guard.service                     # optional: hold XMRig while P2Pool is not synced (spec §7.1)
```

`start` returns as soon as the processes are launched; sync and the first shares show in `status`/`logs`, not in the exit code.

## 4a. Switching the sidechain (mini ↔ nano ↔ main)

One line in `p2pool.conf` (`mini = 1` / `nano = 1` / nothing for main), then `sudo moneroid stop p2pool`, delete `/var/lib/moneroid/p2pool/p2pool_peers.txt` and `p2pool.cache` (they hold the peers and blocks of the previous chain — otherwise P2Pool bans the old peers for hours and "synchronizes" an empty chain of its own), `sudo moneroid start p2pool`. Chain parameters from the 4.18 sources: main and mini — a share every 10 s, PPLNS window 2160 shares ≈ 6 h; nano — a share every 30 s, window 2160 shares ≈ 18 h; the minimum share difficulty is 100 000 everywhere.

## 4b. P2Pool over I2P (optional)

`install.sh --i2p --node …` does what P2Pool's `docs/I2P.MD` describes: installs `i2pd`, creates the server tunnel `moneroid-p2pool` on the chain's p2p port (37889 main / 37888 mini / 37890 nano; keys in `/var/lib/i2pd/moneroid-p2pool.dat` — back them up), takes the `.b32.i2p` address from the i2pd web console and appends to `p2pool.conf`: `socks5 = 127.0.0.1:4447`, `socks5-proxy-type = i2p`, `no-dns = 1`, `i2p-address = …`, `p2p = 127.0.0.1:PORT`, `no-clearnet-p2p = 1`. A limitation of P2Pool itself: **all** non-private connections go through the proxy while loopback/LAN addresses go direct, so the Monero node must be either local/on the LAN (`--node 192.168.x.x:RPC:ZMQ`) or inside I2P (a `.b32.i2p` with RPC and ZMQ). `moneroid node`/`doctor` follow the same rule. The I2P address is publicly tied to the wallet in the shares you find — do not reuse an existing address from other services. XMRig is not affected by I2P (it talks to `127.0.0.1:3333`). The first i2p peers appear a few minutes after i2pd starts (tunnel building); sidechain sync over I2P is noticeably slower than over clearnet.

An already installed i2pd is left as is: the script only adds its own `moneroid.conf` to the tunnels directory (`tunnelsdir` from `i2pd.conf`, by default `/etc/i2pd/tunnels.conf.d`), reads the console and SOCKS endpoints from `i2pd.conf`, and restarts i2pd only when the tunnel file was created or changed.

On a filtering uplink (see `troubleshooting.md`) i2pd may fail to reseed (`NetDbReq: No known routers, reseed seems to be totally failed` in the i2pd log): add `proxy = socks://127.0.0.1:1080` (any working SOCKS/HTTP proxy) under `[reseed]` in `/etc/i2pd/i2pd.conf` and restart i2pd — the proxy is needed only for the first reseed. The network is ready when the console (`http://127.0.0.1:7070/`) shows `Network status: OK`, usually within 3–10 minutes. If after 10+ minutes it still shows `Tunnel creation success rate: 0%`, the I2P transports (NTCP2/SSU2) are filtered too and I2P will not work on that uplink without a VPN; that was the case on the Zeonux stand (2026-09-17).

Installation without GitHub access: put the four release files (`moneroid-vX-linux-amd64`, `moneroid-vX-extras.tar.gz`, `p2pool-vY-linux-x64.tar.gz`, `xmrig-Z-linux-static-x64.tar.gz`) into `/var/cache/moneroid/` (or `MONEROID_CACHE=/absolute/path`; it and all its parents must be root-owned and not writable by group/others, otherwise the cache is ignored) — the script takes them from there and still verifies the SHA256; verified downloads are stored there by the script itself.

Remote node + I2P: forward its RPC and ZMQ to a local address (for example `ssh -N -L 127.0.0.1:18089:NODE:18089 -L 127.0.0.1:18083:NODE:18083 …` through a trusted host, or `systemd-socket-proxyd`) and pass `--node 127.0.0.1:18089:18083` — for P2Pool that is a local address and goes direct, while the peers go through I2P. This is how the Zeonux stand is set up.

## 5. Control without sudo (optional)

```sh
install -o root -g root -m 0644 examples/polkit/50-moneroid.rules /etc/polkit-1/rules.d/
```

The rule allows group `moneroid` only `start/stop/restart` (and `reset-failed`) of the two units, and not the service accounts `moneroid-p2pool`/`moneroid-xmrig`, which are in that group for their files. Other units and other actions still require authentication. Needs polkit ≥ 0.106 (JS rules, directory `/etc/polkit-1/rules.d`): Debian 12+/Arch — yes; Ubuntu 22.04 (polkit 0.105) — no, control goes through `sudo` there.

## 6. Hugepages and MSR (administrative, optional)

The services run as unprivileged users, so XMRig applies no MSR mod and cannot allocate hugepages itself. Host tuning is the administrator's call:

```sh
printf 'vm.nr_hugepages = 1536\n' > /etc/sysctl.d/90-moneroid-hugepages.conf   # 3 GiB: XMRig ~1172 pages + P2Pool light ~270
```

`install.sh --hugepages` computes the value from the machine (a RandomX dataset per NUMA node + cache + scratchpads + P2Pool light mode + margin) and installs the MSR unit on Intel.

**MSR (Intel only, optional).** As an unprivileged user XMRig prints `FAILED TO APPLY MSR MOD`. The same effect comes from the oneshot unit `examples/moneroid-msr.service` (needs `msr-tools`): `wrmsr -a 0x1a4 0xf` before XMRig starts, rollback in `ExecStop`. Measured on Zeonux (2×Xeon E5-2683 v4): 13.8 → 14.4 kH/s (+4.5 %). AMD uses different values — do not use this unit there.

Hugepages are reliably applied only at boot (on a running system memory is fragmented and `sysctl -w` allocates only part). P2Pool keeps a RandomX dataset of its own (2 GB, ~1200 hugepages); the example `p2pool.conf` enables `light-mode = 1` so the pages go to XMRig. Check: `moneroid doctor` → `XMRIG_HUGEPAGES pass`, and `huge pages 100%` in the XMRig journal. Without it the hashrate is lower; Moneroid only shows this.

## Rollback

`sh install.sh --uninstall` stops and disables the guard and the two units, removes the unit files, the polkit rule, the sysctl file, the MSR unit, the i2pd tunnel and the binaries; `--purge` also removes `/etc/moneroid`, `/var/lib/moneroid`, the users and the group.
