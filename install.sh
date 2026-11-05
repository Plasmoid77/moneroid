#!/bin/sh
# Moneroid installer: one P2Pool + XMRig pair under systemd from the official
# releases, verified against SHA256 pinned below. POSIX sh, idempotent, Linux
# x86_64 with systemd (Debian/Ubuntu/Arch tested). Re-runs itself under sudo.
#
#   sh install.sh --wallet 4…                       # mini sidechain, node picked from nodes.txt
#   sh install.sh --wallet 4… --node HOST:RPC:ZMQ   # fixed Monero node
#   sh install.sh --wallet 4… --enable --hugepages  # autostart (incl. moneroid-guard) + sysctl/MSR tuning
#   sh install.sh --wallet 4… --i2p --node LAN_IP:RPC:ZMQ   # P2Pool p2p over I2P (i2pd); node must be private or .b32.i2p
#   sh install.sh --uninstall [--purge]             # remove services (and configs/state with --purge)
#
# Existing files in /etc/moneroid are never overwritten; delete them to re-seed.
# Verified downloads are cached in /var/cache/moneroid (MONEROID_CACHE); pre-fill it to install offline.
set -eu

MONEROID_VERSION=v0.5.0
MONEROID_SHA256=b88538d79a00f5c1d83af41188c8392e9cd6aaee1e5f58fa5faebaf1ff839bcf
EXTRAS_SHA256=c0d9f246cea343b669cf7dfde62362b6444ffac45e4ac2007051590b25eac09a
P2POOL_VERSION=v4.18
P2POOL_SHA256=893691726b0218fe1883a7a326e2c69db4eb228fc72ba00c8adfa6be85b8a415 # from sha256sums.txt.asc, signature checked
XMRIG_VERSION=6.26.0
XMRIG_SHA256=fc6f8ae5f64e4f17481f7e3be29a1c56949f216a998414188003eae1db20c9e5 # from SHA256SUMS

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
case " $* " in *" -h "* | *" --help "*) usage ;; esac
[ "$(id -u)" -eq 0 ] || exec sudo -- sh "$0" --user "$(id -un)" "$@"

WALLET='' SIDECHAIN=mini NODE='' ENABLE=0 HUGEPAGES=0 I2P=0 OPERATOR=${SUDO_USER:-} UNINSTALL=0 PURGE=0
while [ $# -gt 0 ]; do
	case $1 in
	--wallet) WALLET=$2; shift ;;
	--sidechain) SIDECHAIN=$2; shift ;;
	--node) NODE=$2; shift ;;
	--user) OPERATOR=$2; shift ;;
	--enable) ENABLE=1 ;;
	--hugepages) HUGEPAGES=1 ;;
	--i2p) I2P=1 ;;
	--uninstall) UNINSTALL=1 ;;
	--purge) PURGE=1 ;;
	-h | --help) usage ;;
	*) echo "unknown argument: $1" >&2; usage ;;
	esac
	shift
done

die() { echo "install.sh: $*" >&2; exit 1; }
# i2pd.conf readers: comments stripped, "key = value" or "key=value", indented lines tolerated.
i2pd_ep() { # section default-address:port → address:port
	awk -v want="$1" -v def="$2" '
		{ sub(/[#;].*/, ""); gsub(/^[ \t]+|[ \t]+$/, "") }
		/^\[.*\]$/ { sec = substr($0, 2, length($0) - 2); next }
		sec == want && $0 ~ /^(address|port)[ \t]*=/ { k = $0; sub(/[ \t]*=.*/, "", k); v = $0; sub(/^[^=]*=[ \t]*/, "", v); if (k == "address") a = v; else p = v }
		END { split(def, d, ":"); if (a == "") a = d[1]; if (p == "") p = d[2]; print a ":" p }' /etc/i2pd/i2pd.conf 2>/dev/null || echo "$2"
}
i2pd_tdir() {
	d=$(sed -n 's/^[ \t]*tunnelsdir[ \t]*=[ \t]*//p' /etc/i2pd/i2pd.conf 2>/dev/null | sed 's/[ \t]*[#;].*//; s/[ \t]*$//' | tail -1)
	[ -n "$d" ] || { d=/etc/i2pd/tunnels.conf.d; [ -d $d ] || d=/etc/i2pd/tunnels.d; }
	echo "$d"
}
command -v systemctl >/dev/null || die "systemd is required"
[ "$(uname -m)" = x86_64 ] || die "only Linux x86_64 binaries are pinned here"

UNITS="moneroid-p2pool.service moneroid-xmrig.service"
if [ $UNINSTALL = 1 ]; then
	# The guard goes first so it cannot restart what is being removed.
	for u in moneroid-guard.service $UNITS moneroid-msr.service; do systemctl disable --now "$u" 2>/dev/null || true; done # one missing unit must not skip the rest
	rm -f /etc/systemd/system/moneroid-guard.service /etc/systemd/system/moneroid-p2pool.service /etc/systemd/system/moneroid-xmrig.service \
		/etc/systemd/system/moneroid-msr.service /etc/polkit-1/rules.d/50-moneroid.rules \
		/etc/sysctl.d/90-moneroid-hugepages.conf /usr/local/bin/moneroid /usr/local/bin/p2pool /usr/local/bin/xmrig
	systemctl daemon-reload
	if [ -e "$(i2pd_tdir)/moneroid.conf" ]; then
		rm -f "$(i2pd_tdir)/moneroid.conf"
		systemctl try-restart i2pd 2>/dev/null || true
	fi
	if [ $PURGE = 1 ]; then
		rm -rf /etc/moneroid /var/lib/moneroid /var/lib/i2pd/moneroid-p2pool.dat
		userdel moneroid-p2pool 2>/dev/null || true
		userdel moneroid-xmrig 2>/dev/null || true
		groupdel moneroid 2>/dev/null || true
	fi
	echo "removed; kept /etc/moneroid and /var/lib/moneroid unless --purge"
	exit 0
fi

# Values go into sed programs and config files: allow only the characters they can legitimately contain.
if [ -n "$WALLET" ]; then
	printf '%s' "$WALLET" | grep -Eq '^4[1-9A-HJ-NP-Za-km-z]{94}$' || die "--wallet must be a 95-character base58 primary address starting with 4"
else [ -f /etc/moneroid/p2pool.conf ] || die "--wallet 4… is required (primary address, not a subaddress)"; fi
case $SIDECHAIN in mini | nano | main) ;; *) die "--sidechain must be mini, nano or main" ;; esac
NODE_HOST='' NODE_RPC='' NODE_ZMQ=''
if [ -n "$NODE" ]; then
	case $NODE in
	\[*) NODE_HOST=${NODE%%]*}; NODE_HOST=${NODE_HOST#[}; rest=${NODE##*]:} ;; # [ipv6]:rpc:zmq
	*) NODE_HOST=${NODE%%:*}; rest=${NODE#*:} ;;
	esac
	NODE_RPC=${rest%%:*}; NODE_ZMQ=${rest##*:}
	printf '%s' "$NODE_HOST" | grep -Eq '^[A-Za-z0-9.:-]+$' && printf '%s:%s' "$NODE_RPC" "$NODE_ZMQ" | grep -Eq '^[0-9]{1,5}:[0-9]{1,5}$' \
		|| die "--node must be HOST:RPC:ZMQ (IPv6 as [addr]:RPC:ZMQ), got $NODE"
fi

CACHE=${MONEROID_CACHE:-/var/cache/moneroid} # verified downloads are kept here; pre-fill it to install offline
fetch() { # url dest
	if [ "$2" != - ] && [ -f "$CACHE/${1##*/}" ] && [ ! -L "$CACHE/${1##*/}" ]; then cp "$CACHE/${1##*/}" "$2"; return; fi
	if command -v curl >/dev/null; then curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null; then wget -q -O "$2" "$1"
	else die "curl or wget is required"; fi
}
verify() { # file sha256 url
	echo "$2  $1" | sha256sum -c --quiet - || die "checksum mismatch for $1"
	# cache only into a root-owned, non-writable-by-others directory; install(1) replaces the target instead of following it
	[ -d "$CACHE" ] || install -d -o root -g root -m 0755 "$CACHE" 2>/dev/null || return 0
	[ "$(stat -c %u "$CACHE")" = 0 ] && [ -z "$(find "$CACHE" -maxdepth 0 -perm /022)" ] || { echo "note: $CACHE not root-owned 0755, not caching"; return 0; }
	install -o root -g root -m 0644 "$1" "$CACHE/${3##*/}" 2>/dev/null || true
}

TMP=$(mktemp -d) && trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
echo "downloading Moneroid $MONEROID_VERSION, P2Pool $P2POOL_VERSION, XMRig $XMRIG_VERSION…"
URL_MONEROID="https://github.com/Plasmoid77/moneroid/releases/download/$MONEROID_VERSION/moneroid-$MONEROID_VERSION-linux-amd64"
URL_EXTRAS="https://github.com/Plasmoid77/moneroid/releases/download/$MONEROID_VERSION/moneroid-$MONEROID_VERSION-extras.tar.gz"
URL_P2POOL="https://github.com/SChernykh/p2pool/releases/download/$P2POOL_VERSION/p2pool-$P2POOL_VERSION-linux-x64.tar.gz"
URL_XMRIG="https://github.com/xmrig/xmrig/releases/download/v$XMRIG_VERSION/xmrig-$XMRIG_VERSION-linux-static-x64.tar.gz"
fetch "$URL_MONEROID" moneroid; fetch "$URL_EXTRAS" extras.tar.gz; fetch "$URL_P2POOL" p2pool.tar.gz; fetch "$URL_XMRIG" xmrig.tar.gz
verify moneroid "$MONEROID_SHA256" "$URL_MONEROID"; verify extras.tar.gz "$EXTRAS_SHA256" "$URL_EXTRAS"
verify p2pool.tar.gz "$P2POOL_SHA256" "$URL_P2POOL"; verify xmrig.tar.gz "$XMRIG_SHA256" "$URL_XMRIG"
tar xzf extras.tar.gz
tar xzf p2pool.tar.gz --strip-components=1 --wildcards '*/p2pool'
tar xzf xmrig.tar.gz --strip-components=1 --wildcards '*/xmrig'

NOLOGIN=$(command -v nologin || echo /usr/sbin/nologin)
groupadd -r moneroid 2>/dev/null || true
useradd -r -g moneroid -s "$NOLOGIN" -d /var/lib/moneroid/p2pool -M moneroid-p2pool 2>/dev/null || true
useradd -r -g moneroid -s "$NOLOGIN" -d /var/lib/moneroid/xmrig -M moneroid-xmrig 2>/dev/null || true

install -o root -g root -m 0755 moneroid /usr/local/bin/moneroid
install -o root -g root -m 0755 p2pool /usr/local/bin/p2pool
install -o root -g root -m 0755 xmrig /usr/local/bin/xmrig
install -d -o root -g root -m 0755 /etc/moneroid
install -o root -g root -m 0644 systemd/moneroid-p2pool.service systemd/moneroid-xmrig.service systemd/moneroid-guard.service /etc/systemd/system/
if [ -d /etc/polkit-1/rules.d ]; then install -o root -g root -m 0644 examples/polkit/50-moneroid.rules /etc/polkit-1/rules.d/
else echo "note: polkit without rules.d (< 0.106): start/stop need sudo"; fi

seed() { if [ -e "/etc/moneroid/$1" ]; then echo "keeping existing /etc/moneroid/$1"; else install -o root -g "$2" -m "$3" "examples/$1" "/etc/moneroid/$1"; fi; }
FRESH=0
if [ ! -e /etc/moneroid/p2pool.conf ]; then
	FRESH=1
	sed -e "s/^wallet = .*/wallet = $WALLET/" -e '/^mini = 1$/d' examples/p2pool.conf > p2pool.conf
	[ "$SIDECHAIN" = main ] || printf '%s = 1\n' "$SIDECHAIN" >> p2pool.conf
	install -o root -g moneroid -m 0640 p2pool.conf /etc/moneroid/p2pool.conf
else echo "keeping existing /etc/moneroid/p2pool.conf (wallet/sidechain arguments ignored)"; fi
seed xmrig.json moneroid 0640
seed nodes.txt root 0644
if [ ! -e /etc/moneroid/moneroid.toml ]; then
	sed -e 's|^# params_file|params_file|' -e 's|^# nodes_file|nodes_file|' examples/moneroid.toml > moneroid.toml
	install -o root -g root -m 0644 moneroid.toml /etc/moneroid/moneroid.toml
fi
if [ -n "$NODE" ]; then
	sed -i -e "s/^host = .*/host = $NODE_HOST/" -e "s/^rpc-port = .*/rpc-port = $NODE_RPC/" -e "s/^zmq-port = .*/zmq-port = $NODE_ZMQ/" /etc/moneroid/p2pool.conf
fi

if [ $I2P = 1 ]; then
	if ! command -v i2pd >/dev/null; then
		if command -v apt-get >/dev/null; then { apt-get update -qq && apt-get install -y -qq i2pd; } >/dev/null 2>&1 || true
		elif command -v pacman >/dev/null; then pacman -S --noconfirm --needed i2pd >/dev/null 2>&1 || true; fi
		command -v i2pd >/dev/null || die "i2pd not installed; install it and re-run with --i2p"
	fi
	# P2Pool proxies every non-private connection, so the node in the effective config must be LAN/localhost or .b32.i2p.
	case $(sed -n 's/^host = //p' /etc/moneroid/p2pool.conf | tail -1) in
	10.* | 192.168.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | 127.* | localhost | ::1 | fc* | fd* | fe80:* | *.b32.i2p) ;;
	*) die "--i2p needs a node on LAN/localhost or inside I2P: pass --node LAN_IP:RPC:ZMQ (or a .b32.i2p host)" ;;
	esac
	if grep -q '^nano = 1' /etc/moneroid/p2pool.conf; then p2p_port=37890
	elif grep -q '^mini = 1' /etc/moneroid/p2pool.conf; then p2p_port=37888
	else p2p_port=37889; fi
	# An existing i2pd is left as is: only our own tunnel file is added; console/SOCKS endpoints are read from i2pd.conf.
	console=$(i2pd_ep http 127.0.0.1:7070); socks=$(i2pd_ep socksproxy 127.0.0.1:4447)
	tdir=$(i2pd_tdir); [ -d "$tdir" ] || mkdir -p "$tdir"
	printf '[moneroid-p2pool]\ntype = server\nhost = 127.0.0.1\nport = %s\nkeys = moneroid-p2pool.dat\n' "$p2p_port" > moneroid-tunnel.conf
	if ! cmp -s moneroid-tunnel.conf "$tdir/moneroid.conf"; then
		install -o root -g root -m 0644 moneroid-tunnel.conf "$tdir/moneroid.conf"
		systemctl enable -q i2pd && systemctl restart i2pd
	else systemctl enable -q --now i2pd; fi
	b32=''; for _ in $(seq 1 60); do
		b32=$(fetch "http://$console/?page=i2p_tunnels" - 2>/dev/null | grep -o '>moneroid-p2pool</a>[^<]*[a-z2-7]\{52\}\.b32\.i2p:'"$p2p_port" | grep -o '[a-z2-7]\{52\}\.b32\.i2p' | head -1) && [ -n "$b32" ] && break
		sleep 2
	done
	[ -n "$b32" ] || die "i2pd did not report the moneroid-p2pool tunnel (web console $console); check journalctl -u i2pd"
	sed -i '/^socks5 = \|^socks5-proxy-type = \|^no-dns = \|^i2p-address = \|^p2p = \|^no-clearnet-p2p = /d' /etc/moneroid/p2pool.conf
	printf '\n# P2Pool over I2P (install.sh --i2p): peers via i2pd SOCKS, node on LAN/localhost is reached directly.\nsocks5 = %s\nsocks5-proxy-type = i2p\nno-dns = 1\ni2p-address = %s\np2p = 127.0.0.1:%s\nno-clearnet-p2p = 1\n' "$socks" "$b32" "$p2p_port" >> /etc/moneroid/p2pool.conf
	echo "I2P: p2p tunnel $b32:$p2p_port, keys in /var/lib/i2pd/moneroid-p2pool.dat (back it up)"
fi

if [ $HUGEPAGES = 1 ]; then
	numa=$(ls -d /sys/devices/system/node/node[0-9]* 2>/dev/null | wc -l); [ "$numa" -ge 1 ] || numa=1
	# RandomX dataset per NUMA node (~1040 pages) + cache + scratchpads + P2Pool light-mode + margin
	nr=$((1100 * numa + 200 + $(nproc) + 300))
	printf 'vm.nr_hugepages = %s\n' "$nr" > /etc/sysctl.d/90-moneroid-hugepages.conf
	sysctl -q -p /etc/sysctl.d/90-moneroid-hugepages.conf || true
	echo "hugepages: vm.nr_hugepages=$nr (fully effective after a reboot)"
	if grep -q GenuineIntel /proc/cpuinfo; then
		if ! command -v wrmsr >/dev/null; then
			if command -v apt-get >/dev/null; then { apt-get update -qq && apt-get install -y -qq msr-tools; } >/dev/null 2>&1 || true
			elif command -v pacman >/dev/null; then pacman -S --noconfirm --needed msr-tools >/dev/null 2>&1 || true; fi
		fi
		if command -v wrmsr >/dev/null; then
			install -o root -g root -m 0644 examples/moneroid-msr.service /etc/systemd/system/
			systemctl enable -q --now moneroid-msr.service
		else echo "note: msr-tools not found; MSR unit skipped"; fi
	fi
fi

systemctl daemon-reload
if [ -n "$OPERATOR" ] && [ "$OPERATOR" != root ]; then
	usermod -aG moneroid "$OPERATOR" && echo "added $OPERATOR to group moneroid (re-login to read the Data API)"
fi

# Only a freshly seeded config still points at the example node; an existing 127.0.0.1 may be a real local monerod.
if [ $FRESH = 1 ] && [ -z "$NODE" ] && [ $I2P = 0 ]; then
	echo "probing Monero nodes from /etc/moneroid/nodes.txt…"
	moneroid node select || echo "no usable node found; set host/rpc-port/zmq-port in /etc/moneroid/p2pool.conf and run: moneroid start"
fi
if systemctl is-active -q moneroid-p2pool.service moneroid-xmrig.service; then moneroid restart; else moneroid start; fi
if [ $ENABLE = 1 ]; then systemctl enable -q $UNITS moneroid-guard.service && echo "autostart enabled"; fi
# The guard keeps XMRig stopped while P2Pool is not synced; an upgrade restarts it, --enable starts it.
if systemctl is-active -q moneroid-guard.service; then systemctl restart moneroid-guard.service
elif [ $ENABLE = 1 ]; then systemctl start moneroid-guard.service; fi
echo; moneroid doctor || true
echo; echo "done: moneroid status | moneroid tui | moneroid logs --follow p2pool  (sidechain sync takes a few minutes)"
