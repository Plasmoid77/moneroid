#!/bin/sh
# Moneroid installer: one P2Pool + XMRig pair under systemd from the newest
# official releases, verified by their authors' signatures. POSIX sh, idempotent,
# Linux x86_64 with systemd (Debian/Ubuntu/Arch tested). Re-runs itself under sudo.
#
#   sh install.sh --wallet 4…                       # mini sidechain, node picked from nodes.txt
#   sh install.sh --wallet 4… --node HOST:RPC:ZMQ   # fixed Monero node
#   sh install.sh --wallet 4… --enable --hugepages  # autostart (incl. moneroid-guard) + sysctl/MSR tuning
#   sh install.sh --wallet 4… --i2p --node LAN_IP:RPC:ZMQ   # P2Pool p2p over I2P (i2pd); node must be private or .b32.i2p
#   sh install.sh                                   # re-run = update everything to the newest releases
#   sh install.sh --p2pool-version v4.18 --xmrig-version 6.26.0 --moneroid-version v0.5.1  # pin (rollback, offline)
#   sh install.sh --uninstall [--purge]             # remove services (and configs/state with --purge)
#
# Existing files in /etc/moneroid are never overwritten; delete them to re-seed.
# Verified downloads are cached in /var/cache/moneroid (MONEROID_CACHE); with it pre-filled and all
# three versions given, the install works offline.
set -eu

# A download is accepted only if its SHA256 is listed in a checksum file signed by the
# primary key pinned here (P2Pool, XMRig); the public keys ship in keys/ of the Moneroid release.
# Moneroid itself is checked against SHA256SUMS of its release (same trust as this script).
P2POOL_KEY=1FCAAB4D3DC3310D16CBD508C47F82B54DA87ADF # SChernykh, as in monero-project/gitian.sigs
XMRIG_KEY=9AC4CEA8E66E35A5C7CDDC1B446A53638BE94409  # XMRig <support@xmrig.com>, xmrig/xmrig doc/gpg_keys

usage() { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
case " $* " in *" -h "* | *" --help "*) usage ;; esac
[ "$(id -u)" -eq 0 ] || exec sudo -- sh "$0" --user "$(id -un)" "$@"

WALLET='' SIDECHAIN=mini NODE='' ENABLE=0 HUGEPAGES=0 I2P=0 OPERATOR=${SUDO_USER:-} UNINSTALL=0 PURGE=0
MONEROID_VERSION='' P2POOL_VERSION='' XMRIG_VERSION='' # empty = the newest stable release
while [ $# -gt 0 ]; do
	case $1 in
	--wallet) WALLET=$2; shift ;;
	--sidechain) SIDECHAIN=$2; shift ;;
	--node) NODE=$2; shift ;;
	--user) OPERATOR=$2; shift ;;
	--moneroid-version) MONEROID_VERSION=$2; shift ;;
	--p2pool-version) P2POOL_VERSION=$2; shift ;;
	--xmrig-version) XMRIG_VERSION=$2; shift ;;
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
NL='
'
ok() { # value extended-regex: the whole value matches. grep matches line by line, so a value with a
	# newline would pass on one good line and carry the rest into the sed programs below.
	case $1 in *"$NL"*) return 1 ;; esac
	printf '%s' "$1" | grep -Eq "$2"
}
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
[ "$(uname -m)" = x86_64 ] || die "only Linux x86_64 release binaries are installed here"

UNITS="moneroid-p2pool.service moneroid-xmrig.service"
if [ $UNINSTALL = 1 ]; then
	# The guard goes first so it cannot restart what is being removed.
	for u in moneroid-guard.service $UNITS moneroid-msr.service; do systemctl disable --now "$u" 2>/dev/null || true; done # one missing unit must not skip the rest
	# The MSR helper stays while values it saved are still unrestored: it is the way back (or reboot).
	if [ -e /run/moneroid-msr.saved ]; then echo "note: MSR values not restored; kept moneroid-msr and its unit (reboot, then re-run --uninstall)"
	else rm -f /etc/systemd/system/moneroid-msr.service /usr/local/libexec/moneroid-msr; fi
	rm -f /etc/systemd/system/moneroid-guard.service /etc/systemd/system/moneroid-p2pool.service /etc/systemd/system/moneroid-xmrig.service \
		/etc/polkit-1/rules.d/50-moneroid.rules \
		/etc/sysctl.d/90-moneroid-hugepages.conf /usr/local/bin/moneroid /usr/local/bin/p2pool /usr/local/bin/xmrig \
		/var/lib/moneroid/install-restart-pending
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
	ok "$WALLET" '^4[1-9A-HJ-NP-Za-km-z]{94}$' || die "--wallet must be a 95-character base58 primary address starting with 4"
else [ -f /etc/moneroid/p2pool.conf ] || die "--wallet 4… is required (primary address, not a subaddress)"; fi
case $SIDECHAIN in mini | nano | main) ;; *) die "--sidechain must be mini, nano or main" ;; esac
NODE_HOST='' NODE_RPC='' NODE_ZMQ=''
if [ -n "$NODE" ]; then
	case $NODE in
	\[*) NODE_HOST=${NODE%%]*}; NODE_HOST=${NODE_HOST#[}; rest=${NODE##*]:} ;; # [ipv6]:rpc:zmq
	*) NODE_HOST=${NODE%%:*}; rest=${NODE#*:} ;;
	esac
	NODE_RPC=${rest%%:*}; NODE_ZMQ=${rest##*:}
	ok "$NODE_HOST" '^[A-Za-z0-9.:-]+$' && ok "$NODE_RPC:$NODE_ZMQ" '^[0-9]{1,5}:[0-9]{1,5}$' \
		|| die "--node must be HOST:RPC:ZMQ (IPv6 as [addr]:RPC:ZMQ), got $NODE"
fi

CACHE=${MONEROID_CACHE:-/var/cache/moneroid} # verified downloads are kept here; pre-fill it to install offline
safe() { # path: root-owned and not writable by group/others (a cache anyone else can write could plant a binary)
	[ -e "$1" ] && [ ! -L "$1" ] && [ "$(stat -c %u "$1")" = 0 ] && [ -z "$(find "$1" -maxdepth 0 -perm /022)" ]
}
safe_dir() { # absolute dir: it and every ancestor safe, so nobody but root can swap a directory on the path
	case $1 in /*) ;; *) return 1 ;; esac
	d=$1
	while :; do safe "$d" || return 1; [ "$d" = / ] && return 0; d=$(dirname "$d"); done
}
fetch() { # url dest [cache-name]: the cached copy if there is a trustworthy one, else download
	if [ -n "${3:-}" ] && [ -f "$CACHE/$3" ] && safe_dir "$CACHE" && safe "$CACHE/$3"; then cp "$CACHE/$3" "$2"; return; fi
	if command -v curl >/dev/null; then curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null; then wget -q -O "$2" "$1"
	else die "curl or wget is required"; fi
}
keep() { # file cache-name: cache only into a root-owned, non-writable-by-others directory; install(1) replaces the target instead of following it
	[ -d "$CACHE" ] || install -d -o root -g root -m 0755 "$CACHE" 2>/dev/null || return 0
	safe_dir "$CACHE" || { echo "note: $CACHE or a parent is not root-owned and closed to others, not caching"; return 0; }
	install -o root -g root -m 0644 "$1" "$CACHE/$2" 2>/dev/null || true
}
latest() { # owner/repo option → tag of the newest stable release (GitHub skips drafts and pre-releases)
	t=$(fetch "https://api.github.com/repos/$1/releases/latest" - | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/') || true
	[ -n "$t" ] || die "cannot ask GitHub for the newest $1 release (offline? pass $2)"
	echo "$t"
}
verify() { # file sha256 cache-name
	ok "$2" '^[0-9a-f]{64}$' || die "no SHA256 for $1 in the signed checksum list"
	echo "$2  $1" | sha256sum -c --quiet - || die "checksum mismatch for $1"
	keep "$1" "$3"
}
sum_of() { # file-name list: "HASH  NAME" / "HASH *NAME" lines (sha256sum) or "Name:"/"SHA256:" pairs (P2Pool)
	awk -v f="$1" '$1 == "Name:" { n = $2 } $1 == "SHA256:" && n == f { print tolower($2); exit }
		NF == 2 && length($1) == 64 && $1 !~ /[^0-9A-Fa-f]/ && ($2 == f || $2 == "*" f) { print tolower($1); exit }' "$2"
}
signed() { # primary-key-fingerprint gpg-arguments…: a good signature by exactly that key
	k=$1; shift
	gpg --batch --no-tty --quiet --status-file gpg.status "$@" 2>/dev/null && grep -Eq "^\[GNUPG:\] VALIDSIG .* $k\$" gpg.status &&
		! grep -Eq '^\[GNUPG:\] (BADSIG|ERRSIG|REVKEYSIG|KEYREVOKED|EXPKEYSIG|KEYEXPIRED)' gpg.status
}
installed() { # binary → its installed version (v1.2.3), empty if none
	o=--version; [ "$1" != moneroid ] || o=version
	[ -x "/usr/local/bin/$1" ] && "/usr/local/bin/$1" $o 2>/dev/null | head -1 | grep -Eo 'v?[0-9]+(\.[0-9]+){1,3}' | head -1 | sed 's/^v*/v/' || true
}
older() { # a b: version a is lower than b
	[ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$1" ]
}
if ! command -v gpg >/dev/null; then
	if command -v apt-get >/dev/null; then { apt-get update -qq && apt-get install -y -qq gpg; } >/dev/null 2>&1 || true
	elif command -v pacman >/dev/null; then pacman -S --noconfirm --needed gnupg >/dev/null 2>&1 || true; fi
	command -v gpg >/dev/null || die "gpg is required to check the release signatures; install gnupg and re-run"
fi
for v in "$MONEROID_VERSION" "$P2POOL_VERSION" "$XMRIG_VERSION"; do # they go into URLs and file names
	[ -z "$v" ] || ok "$v" '^v?[0-9]+(\.[0-9]+){1,3}$' || die "a version looks like v1.2.3, got $v"
done
# "latest" is whatever the upstream repository marks so; a repository taken over without the signing
# key could mark an old, genuinely signed release. Without an explicit --*-version never go lower.
pick() { # name current-value owner/repo option installed-binary
	if [ -n "$2" ]; then echo "v${2#v}"; return; fi
	t=$(latest "$3" "$4"); t=v${t#v}
	ok "$t" '^v[0-9]+(\.[0-9]+){1,3}$' || die "unexpected $1 release tag $t (pass $4)"
	i=$(installed "$5")
	[ -z "$i" ] || ! older "$t" "$i" || die "the newest $1 release $t is older than the installed $i; pass $4 $t to downgrade"
	echo "$t"
}
MONEROID_VERSION=$(pick Moneroid "$MONEROID_VERSION" Plasmoid77/moneroid --moneroid-version moneroid)
P2POOL_VERSION=$(pick P2Pool "$P2POOL_VERSION" SChernykh/p2pool --p2pool-version p2pool)
XMRIG_VERSION=$(pick XMRig "$XMRIG_VERSION" xmrig/xmrig --xmrig-version xmrig); XMRIG_VERSION=${XMRIG_VERSION#v}

# RESTART: services whose binary or unit this run replaced, plus those a failed earlier run left
# in PENDING. Only they are restarted at the end, and only if running: a Moneroid-only update
# costs no P2Pool sidechain resync.
REPLACED=0 RESTART='' PENDING=/var/lib/moneroid/install-restart-pending
if [ -f "$PENDING" ] && [ ! -L "$PENDING" ]; then
	for u in $(cat "$PENDING"); do case $u in moneroid-p2pool.service | moneroid-xmrig.service) RESTART="$RESTART $u" ;; esac; done
fi
restarts() { # RESTART, plus P2Pool when p2pool.conf differs from before this run
	r=$RESTART
	[ ! -e "$TMP/p2pool.conf.before" ] || cmp -s "$TMP/p2pool.conf.before" /etc/moneroid/p2pool.conf || r="$r moneroid-p2pool.service"
	echo $r
}
cleanup() {
	rc=$?
	if [ $rc != 0 ] && [ $REPLACED = 1 ]; then # the new files are on disk, the services still run the old ones
		systemctl daemon-reload 2>/dev/null || true
		r=$(restarts); [ -z "$r" ] || { install -d -o root -g root -m 0755 /var/lib/moneroid && printf '%s\n' $r > "$PENDING"; } || true
		echo "install.sh: stopped after the binaries and units were replaced; services were not restarted. Fix the cause and re-run (it restarts them)." >&2
	fi
	gpgconf --kill all 2>/dev/null; rm -rf "$TMP"
}
TMP=$(mktemp -d) && trap cleanup EXIT
cd "$TMP"
GNUPGHOME=$TMP/gnupg && export GNUPGHOME && install -d -m 0700 "$GNUPGHOME"
echo "installing Moneroid $MONEROID_VERSION, P2Pool $P2POOL_VERSION, XMRig $XMRIG_VERSION…"
REL=https://github.com/Plasmoid77/moneroid/releases/download/$MONEROID_VERSION
M_BIN=moneroid-$MONEROID_VERSION-linux-amd64 M_EXTRAS=moneroid-$MONEROID_VERSION-extras.tar.gz
fetch "$REL/SHA256SUMS" moneroid.sums "moneroid-$MONEROID_VERSION-SHA256SUMS" || die "Moneroid $MONEROID_VERSION has no SHA256SUMS (v0.5.1 or newer is needed)"
fetch "$REL/$M_BIN" moneroid "$M_BIN"; fetch "$REL/$M_EXTRAS" extras.tar.gz "$M_EXTRAS"
verify moneroid "$(sum_of "$M_BIN" moneroid.sums)" "$M_BIN"; verify extras.tar.gz "$(sum_of "$M_EXTRAS" moneroid.sums)" "$M_EXTRAS"
keep moneroid.sums "moneroid-$MONEROID_VERSION-SHA256SUMS"
tar xzf extras.tar.gz systemd examples keys docs # only these members: nothing may land next to gnupg/ or gpg.status
[ -f keys/p2pool-SChernykh.asc ] && [ -f keys/xmrig.asc ] || die "Moneroid $MONEROID_VERSION has no keys/ (v0.5.1 or newer is needed)"
gpg --batch --quiet --import keys/p2pool-SChernykh.asc keys/xmrig.asc 2>/dev/null || die "cannot import the signing keys from keys/"

P_TGZ=p2pool-$P2POOL_VERSION-linux-x64.tar.gz
fetch "https://github.com/SChernykh/p2pool/releases/download/$P2POOL_VERSION/sha256sums.txt.asc" p2pool.asc "p2pool-$P2POOL_VERSION-sha256sums.txt.asc"
signed "$P2POOL_KEY" --output p2pool.sums --decrypt p2pool.asc || die "P2Pool $P2POOL_VERSION: sha256sums.txt.asc is not signed by $P2POOL_KEY"
keep p2pool.asc "p2pool-$P2POOL_VERSION-sha256sums.txt.asc"
fetch "https://github.com/SChernykh/p2pool/releases/download/$P2POOL_VERSION/$P_TGZ" p2pool.tar.gz "$P_TGZ"
verify p2pool.tar.gz "$(sum_of "$P_TGZ" p2pool.sums)" "$P_TGZ" # p2pool.sums is the signed text only

X_REL=https://github.com/xmrig/xmrig/releases/download/v$XMRIG_VERSION X_TGZ=xmrig-$XMRIG_VERSION-linux-static-x64.tar.gz
fetch "$X_REL/SHA256SUMS" xmrig.sums "xmrig-$XMRIG_VERSION-SHA256SUMS"; fetch "$X_REL/SHA256SUMS.sig" xmrig.sig "xmrig-$XMRIG_VERSION-SHA256SUMS.sig"
signed "$XMRIG_KEY" --verify xmrig.sig xmrig.sums || die "XMRig $XMRIG_VERSION: SHA256SUMS is not signed by $XMRIG_KEY"
keep xmrig.sums "xmrig-$XMRIG_VERSION-SHA256SUMS"; keep xmrig.sig "xmrig-$XMRIG_VERSION-SHA256SUMS.sig"
fetch "$X_REL/$X_TGZ" xmrig.tar.gz "$X_TGZ"
verify xmrig.tar.gz "$(sum_of "$X_TGZ" xmrig.sums)" "$X_TGZ"
tar xzf p2pool.tar.gz --strip-components=1 --wildcards '*/p2pool'
tar xzf xmrig.tar.gz --strip-components=1 --wildcards '*/xmrig'

# Everything that can be refused is refused before the first installed file is replaced.
if [ $I2P = 1 ]; then
	if ! command -v i2pd >/dev/null; then
		if command -v apt-get >/dev/null; then { apt-get update -qq && apt-get install -y -qq i2pd; } >/dev/null 2>&1 || true
		elif command -v pacman >/dev/null; then pacman -S --noconfirm --needed i2pd >/dev/null 2>&1 || true; fi
		command -v i2pd >/dev/null || die "i2pd not installed; install it and re-run with --i2p"
	fi
	# P2Pool proxies every non-private connection, so the node it will use must be LAN/localhost or .b32.i2p:
	# --node, else the existing config, else the example a fresh config is seeded from.
	host=$NODE_HOST; conf=/etc/moneroid/p2pool.conf; [ -e $conf ] || conf=examples/p2pool.conf
	[ -n "$host" ] || host=$(sed -n 's/^host = //p' $conf | tail -1)
	case $host in
	10.* | 192.168.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | 127.* | localhost | ::1 | fc* | fd* | fe80:* | *.b32.i2p) ;;
	*) die "--i2p needs a node on LAN/localhost or inside I2P: pass --node LAN_IP:RPC:ZMQ (or a .b32.i2p host)" ;;
	esac
fi

NOLOGIN=$(command -v nologin || echo /usr/sbin/nologin)
groupadd -r moneroid 2>/dev/null || true
useradd -r -g moneroid -s "$NOLOGIN" -d /var/lib/moneroid/p2pool -M moneroid-p2pool 2>/dev/null || true
useradd -r -g moneroid -s "$NOLOGIN" -d /var/lib/moneroid/xmrig -M moneroid-xmrig 2>/dev/null || true

put() { # file mode target [service]: install only when the content differs
	cmp -s "$1" "$3" && return 0
	install -o root -g root -m "$2" "$1" "$3"
	[ -z "${4:-}" ] || RESTART="$RESTART $4"
}
REPLACED=1
put moneroid 0755 /usr/local/bin/moneroid
put p2pool 0755 /usr/local/bin/p2pool moneroid-p2pool.service
put xmrig 0755 /usr/local/bin/xmrig moneroid-xmrig.service
install -d -o root -g root -m 0755 /etc/moneroid
for u in $UNITS; do put "systemd/$u" 0644 "/etc/systemd/system/$u" "$u"; done
put systemd/moneroid-guard.service 0644 /etc/systemd/system/moneroid-guard.service
if [ -d /etc/polkit-1/rules.d ]; then install -o root -g root -m 0644 examples/polkit/50-moneroid.rules /etc/polkit-1/rules.d/
else echo "note: polkit without rules.d (< 0.106): start/stop need sudo"; fi

cp /etc/moneroid/p2pool.conf p2pool.conf.before 2>/dev/null || : > p2pool.conf.before # restarts() compares
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
	# Same block every run (comment included), so a repeated --i2p leaves p2pool.conf byte-identical.
	sed -i '/^# P2Pool over I2P (install.sh --i2p)/d; /^socks5 = \|^socks5-proxy-type = \|^no-dns = \|^i2p-address = \|^p2p = \|^no-clearnet-p2p = /d' /etc/moneroid/p2pool.conf
	printf '# P2Pool over I2P (install.sh --i2p): peers via i2pd SOCKS, node on LAN/localhost is reached directly.\nsocks5 = %s\nsocks5-proxy-type = i2p\nno-dns = 1\ni2p-address = %s\np2p = 127.0.0.1:%s\nno-clearnet-p2p = 1\n' "$socks" "$b32" "$p2p_port" >> /etc/moneroid/p2pool.conf
	echo "I2P: p2p tunnel $b32:$p2p_port, keys in /var/lib/i2pd/moneroid-p2pool.dat (back it up)"
fi

if [ $HUGEPAGES = 1 ]; then
	numa=$(ls -d /sys/devices/system/node/node[0-9]* 2>/dev/null | wc -l); [ "$numa" -ge 1 ] || numa=1
	# RandomX dataset per NUMA node (~1040 pages) + cache + scratchpads + P2Pool light-mode + margin
	nr=$((1100 * numa + 200 + $(nproc) + 300))
	printf 'vm.nr_hugepages = %s\n' "$nr" > /etc/sysctl.d/90-moneroid-hugepages.conf
	sysctl -q -p /etc/sysctl.d/90-moneroid-hugepages.conf || true
	echo "hugepages: vm.nr_hugepages=$nr (fully effective after a reboot)"
	if sh examples/moneroid-msr preset >/dev/null 2>&1; then # Intel or AMD Ryzen Zen 1–5
		if ! command -v wrmsr >/dev/null || ! command -v rdmsr >/dev/null; then
			if command -v apt-get >/dev/null; then { apt-get update -qq && apt-get install -y -qq msr-tools; } >/dev/null 2>&1 || true
			elif command -v pacman >/dev/null; then pacman -S --noconfirm --needed msr-tools >/dev/null 2>&1 || true; fi
		fi
		if command -v wrmsr >/dev/null && command -v rdmsr >/dev/null; then
			msr=/etc/systemd/system/moneroid-msr.service msr_bin=/usr/local/libexec/moneroid-msr
			if ! cmp -s examples/moneroid-msr.service $msr || ! cmp -s examples/moneroid-msr $msr_bin; then
				# Stopped with the unit systemd still has loaded, so its own stop command undoes its own
				# values (the Intel-only unit before v0.6.1 had no saved values to restore). Replaced only
				# after a clean stop: a new apply would otherwise save modded values as the originals.
				was=$(systemctl is-active moneroid-msr.service 2>/dev/null) || :
				systemctl reset-failed moneroid-msr.service 2>/dev/null || :
				[ "$was" != active ] || systemctl stop moneroid-msr.service 2>/dev/null || :
				if systemctl is-active -q moneroid-msr.service || systemctl is-failed -q moneroid-msr.service || [ -e /run/moneroid-msr.saved ]; then
					die "moneroid-msr.service did not restore the MSR values cleanly; see journalctl -u moneroid-msr, reboot and re-run"
				fi
				install -d -o root -g root -m 0755 /usr/local/libexec
				install -o root -g root -m 0755 examples/moneroid-msr $msr_bin
				install -o root -g root -m 0644 examples/moneroid-msr.service $msr
				systemctl daemon-reload
			fi
			systemctl enable -q --now moneroid-msr.service || echo "note: moneroid-msr.service failed; see journalctl -u moneroid-msr"
		else echo "note: msr-tools not found; MSR unit skipped"; fi
	fi
fi

systemctl daemon-reload
if [ -n "$OPERATOR" ] && [ "$OPERATOR" != root ]; then
	# moneroid: the Data API and configs; systemd-journal: logs and payouts (moneroid logs/payouts, TUI)
	groups=moneroid; getent group systemd-journal >/dev/null && groups=moneroid,systemd-journal
	usermod -aG "$groups" "$OPERATOR" && echo "added $OPERATOR to groups $groups (log in again to use them)"
fi

# Only a freshly seeded config still points at the example node; an existing 127.0.0.1 may be a real local monerod.
if [ $FRESH = 1 ] && [ -z "$NODE" ] && [ $I2P = 0 ]; then
	echo "probing Monero nodes from /etc/moneroid/nodes.txt…"
	moneroid node select || echo "no usable node found; set host/rpc-port/zmq-port in /etc/moneroid/p2pool.conf and run: moneroid start"
fi
# Nothing running: start both. Otherwise restart what changed, only if it runs, through
# `moneroid restart` (serialized with the guard): an XMRig held by the guard or stopped by
# the user stays stopped and starts later with the new files.
R=$(restarts)
if ! systemctl is-active -q $UNITS; then moneroid start
elif [ -z "$R" ]; then echo "binaries, units and p2pool.conf unchanged: the running services are left as they are"
else
	for t in p2pool xmrig; do
		case " $R " in *" moneroid-$t.service "*)
			if systemctl is-active -q "moneroid-$t.service"; then moneroid restart "$t"; fi ;;
		esac
	done
fi
rm -f "$PENDING"
if [ $ENABLE = 1 ]; then systemctl enable -q $UNITS moneroid-guard.service && echo "autostart enabled"; fi
# The guard keeps XMRig stopped while P2Pool is not synced; an upgrade restarts it, --enable starts it.
if systemctl is-active -q moneroid-guard.service; then systemctl restart moneroid-guard.service
elif [ $ENABLE = 1 ]; then systemctl start moneroid-guard.service; fi
echo; moneroid doctor || true
echo; echo "done: moneroid status | moneroid tui | moneroid logs --follow p2pool  (sidechain sync takes a few minutes)"
