# Updating

## Upstream (P2Pool, XMRig)

Moneroid does not need to change as long as the interfaces stay the same: `systemctl show`, `GET /2/summary`, the four Data API files.

```sh
# simplest: re-run install.sh — it fetches the newest releases, checks their signatures and restarts only the running services whose files changed.
# by hand: verify the signature/checksum of the new release (docs/install.md §1), then:
install -o root -g root -m 0755 p2pool /usr/local/bin/p2pool && moneroid restart p2pool
install -o root -g root -m 0755 xmrig  /usr/local/bin/xmrig  && moneroid restart xmrig
moneroid doctor
```

If the binary path changed, edit `ExecStart=` in the unit (drop-in via `systemctl edit`), then `daemon-reload`. If upstream renamed or removed a field, `status` shows `FIELD_INVALID` or `null` for that metric and everything else keeps working.

`install.sh` pins no versions, only the upstream signing keys (spec INSTALL-06): a new upstream release needs no change here. If upstream changes its signing key, the install stops with "not signed by …"; check the new key from an independent source, then update `keys/` and the fingerprint in `install.sh`. A specific version: `--p2pool-version`, `--xmrig-version`, `--moneroid-version`.

## Node

`sudo moneroid node select` (or edit `host/rpc-port/zmq-port` by hand), then `moneroid restart p2pool`. The candidate list `/etc/moneroid/nodes.txt` is your file: nodes disappear, keep it up to date.

## Moneroid

```sh
make build && install -o root -g root -m 0755 moneroid /usr/local/bin/moneroid
```

or re-run `install.sh` (it downloads the newest release and verifies it; a Moneroid-only update restarts the guard, not P2Pool or XMRig). The services do not depend on the Moneroid binary: updating or removing it does not interrupt mining. The `status --json` and `doctor --json` schemas are versioned by `schema_version`; within a version fields are only added.

Releasing: add a `## vX.Y.Z — date` section to `CHANGELOG.md`, merge, then `git tag vX.Y.Z && git push origin vX.Y.Z`. CI runs `make check`, `make dist VERSION=vX.Y.Z` (binary, extras with `keys/`, `SHA256SUMS`) and publishes the release with that CHANGELOG section; `install.sh` picks it up as the newest release. Builds are reproducible (`-trimpath -buildvcs=false`, fixed tar order/owner/mtime), so `make dist` at the tag with the same Go version as the CI job (see its log) yields the same `SHA256SUMS`.
