#!/bin/bash
# End-to-end test of Flock on Linux: in a rootless Podman container per distribution, booting
# systemd (systemd-resolved, a user session with linger, NetworkManager on Ubuntu and Fedora), it
# installs Flock with install.sh from archives built from this tree, checks the setup, NetworkManager,
# drift, a reboot, and uninstall --purge.
#
#   tools/linux-e2e/run.sh [--from <git ref>] [arch] [ubuntu] [fedora]
#
# --from installs that ref first and upgrades from it. resolved.conf has global DNS servers
# (DNS=9.9.9.9 1.1.1.1), the setup that a TLD in the global scope can't handle.
#
# Needs podman and the dummy kernel module, which a rootless container can't load itself:
#   sudo modprobe dummy numdummies=0
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

from=""
if [ "${1:-}" = --from ]; then
  from=$2
  shift 2
fi
distros=${*:-arch ubuntu fedora}

lsmod | grep -qw dummy || {
  echo "the dummy kernel module isn't loaded: sudo modprobe dummy numdummies=0" >&2
  exit 2
}

# The test's files, mounted at /test: install.sh, the archives, and a curl that serves the
# release downloads from them (everything else goes to the real curl).
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# The container's user reads it through the mount (mktemp makes it private).
chmod 755 "$work"
mkdir -p "$work/dist"
cp "$repo/install.sh" "$work/"
cat >"$work/curl" <<'EOF'
#!/bin/sh
for a; do case $prev in -o) out=$a ;; esac; prev=$a; url=$a; done
case $url in
https://github.com/flock-php/flock/releases/download/*) cp "/test/dist/${url##*/}" "$out" ;;
*) exec /usr/bin/curl "$@" ;;
esac
EOF
chmod +x "$work/curl"

build() { # build <version> <source dir>
  make -s -C "$2" -f "$repo/Makefile" linux VERSION="$1" >/dev/null &&
    mv "$2"/dist/flock-"$1"-linux-*.tar.gz* "$work/dist/"
}
echo "building the archives…"
build 0.0.0-e2e "$repo" || exit 1
if [ -n "$from" ]; then
  git -C "$repo" archive "$from" | (mkdir -p "$work/from" && tar -x -C "$work/from")
  build 0.0.0-from "$work/from" || exit 1
fi

total=0
for d in $distros; do
  C=flock-e2e-$d
  fails=0
  echo "######## $d"
  podman build -q -t "flock-e2e-$d" -f "$here/Containerfile.$d" "$here" >/dev/null || exit 1
  nm=false
  if [ "$d" != arch ]; then
    # NetworkManager running; the container's uplink is unmanaged, so what NetworkManager does to
    # flock0 on its own shows. The uplink's drop-in, uplink.conf, sorts after numbered ones and sets
    # unmanaged-devices= (not +=), as admins' files do.
    nm=true
    podman build -q -t "flock-e2e-$d-nm" -f - "$here" >/dev/null <<EOF || exit 1
FROM flock-e2e-$d
RUN (apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends network-manager && rm -rf /var/lib/apt/lists/*) 2>/dev/null || dnf install -y NetworkManager \
 && mkdir -p /etc/NetworkManager/conf.d && printf '[keyfile]\nunmanaged-devices=interface-name:eth*;interface-name:en*\n' > /etc/NetworkManager/conf.d/uplink.conf \
 && systemctl enable NetworkManager
EOF
  fi
  podman rm -f "$C" >/dev/null 2>&1
  # 127.0.0.53 as the resolver, like a desktop with systemd-resolved; ports below 1024 need setcap.
  podman run -d --name "$C" --systemd=always --dns 127.0.0.53 \
    --sysctl net.ipv4.ip_unprivileged_port_start=1024 --cap-add SYS_ADMIN,NET_ADMIN,NET_RAW \
    -v "$work:/test:ro,Z" "flock-e2e-$d$($nm && echo -nm)" >/dev/null || exit 1

  root() { podman exec "$C" sh -c "$1"; }
  boot() {
    # Until systemd has finished booting and the system bus answers.
    for _ in $(seq 60); do
      case $(podman exec "$C" systemctl is-system-running 2>/dev/null) in running | degraded) break ;; esac
      sleep 1
    done
    uid=$(podman exec "$C" id -u dev)
    # logind doesn't start lingering users' managers in a container.
    podman exec "$C" systemctl start "user@$uid"
    for _ in $(seq 30); do
      podman exec "$C" test -S "/run/user/$uid/bus" && break
      sleep 1
    done
  }
  user() {
    podman exec --user dev -e XDG_RUNTIME_DIR="/run/user/$uid" \
      -e DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" -w /home/dev "$C" \
      bash -c "export PATH=/test:\$PATH F=\$HOME/.config/Flock/bin/flock-php; $1"
  }
  check() {
    local name=$1 out
    if out=$("${@:2}" 2>&1); then
      echo "PASS  $name"
    else
      echo "FAIL  $name"
      echo "$out" | sed 's/^/      /' | head -8
      fails=$((fails + 1))
    fi
  }
  resolves() {
    check "$1: hello.test (resolvectl, getent)" root 'resolvectl query hello.test | grep -q 127.0.0.1 && getent hosts hello.test | grep -q 127.0.0.1'
    check "$1: example.com, then hello.test again" root 'getent hosts example.com && for i in 1 2 3; do getent hosts hello.test >/dev/null || exit 1; done'
    check "$1: HTTPS to hello.test (system trust)" user 'curl -sS -o /dev/null https://hello.test/'
  }
  nmstate() { root 'nmcli -t -f DEVICE,STATE device | grep "^flock0:" | cut -d: -f2'; }
  is_unmanaged() { [ "$(nmstate)" = unmanaged ] || {
    nmstate
    return 1
  }; }
  # status_says yes|no <seconds>: waits for `flock-php status` to (not) report flock0.
  status_says() {
    local out
    for _ in $(seq "$2"); do
      # A failing status (the daemon isn't running) says neither.
      out=$(user '$F status' 2>/dev/null) || {
        sleep 1
        continue
      }
      if grep -q flock0 <<<"$out"; then [ "$1" = yes ] && return 0; else [ "$1" = no ] && return 0; fi
      sleep 1
    done
    user '$F status 2>&1 | grep -iA1 "^dns"'
    return 1
  }
  status_says_nm() {
    for _ in $(seq "$1"); do
      user '$F status' 2>/dev/null | grep -q "NetworkManager manages flock0" && return 0
      sleep 1
    done
    user '$F status 2>&1 | grep -iA1 "^dns"'
    return 1
  }

  boot
  # Chrome's NSS database, as a browser that has run once leaves it.
  user 'mkdir -p ~/.pki/nssdb && certutil -d sql:$HOME/.pki/nssdb -N --empty-password'

  if [ -n "$from" ]; then
    check "install $from" user 'FLOCK_VERSION=0.0.0-from sh /test/install.sh </dev/null >/tmp/from.log 2>&1 || { tail -8 /tmp/from.log; exit 1; }'
  fi
  check "install this tree" user 'FLOCK_VERSION=0.0.0-e2e sh /test/install.sh </dev/null >/tmp/install.log 2>&1 || { tail -8 /tmp/install.log; exit 1; }'
  user 'mkdir -p ~/Sites/hello/public && echo "<?php echo 1;" > ~/Sites/hello/public/index.php && cd ~/Sites && $F park >/dev/null'
  sleep 3

  check "user unit active" user 'systemctl --user is-active flock'
  check "flock-php installed, no bin/flock" user 'test -x $F && ! test -e ~/.config/Flock/bin/flock'
  check "util-linux flock first in a new shell" user '[ "$(readlink -f "$(bash -lic "command -v flock" 2>/dev/null)")" = "$(readlink -f /usr/bin/flock)" ]'
  check "flock-php on PATH in a new shell" user 'bash -lic "command -v flock-php" 2>/dev/null | grep -q Flock/bin/flock-php'
  check "flock-dns.service active and enabled" root 'systemctl is-active flock-dns.service && systemctl is-enabled flock-dns.service'
  check "flock0 routes ~test" root 'resolvectl domain flock0 | grep -q "~test" && ip -br addr show flock0 | grep -q 198.18.53.53'
  check "no drop-in in the global scope" root '! ls /etc/systemd/resolved.conf.d/flock-*.conf 2>/dev/null | grep -q .'
  check "NetworkManager drop-in is zz-flock.conf (no 90-flock.conf)" root 'grep -q "interface-name:flock0" /etc/NetworkManager/conf.d/zz-flock.conf && ! test -e /etc/NetworkManager/conf.d/90-flock.conf'
  check "setcap, listening on 80 and 443" user 'getcap $F | grep -q cap_net_bind_service && ss -ltn | grep -q ":80 " && ss -ltn | grep -q ":443 "'
  check "CA in Chrome's NSS database" user 'certutil -d sql:$HOME/.pki/nssdb -L | grep -qi flock'
  resolves "installed"

  if $nm; then
    echo "== NetworkManager"
    check "flock0 unmanaged" is_unmanaged
    root 'nmcli networking off; sleep 2; nmcli networking on; sleep 5; systemctl restart systemd-resolved; sleep 4'
    check "networking off/on + resolved restart: flock0 keeps its address" root 'ip -br addr show flock0 | grep -q 198.18.53.53'
    resolves "networking off/on + resolved restart"
    check "managed yes doesn't take flock0" root 'nmcli device set flock0 managed yes 2>/dev/null; sleep 2; nmcli -t -f DEVICE,STATE device | grep -qx "flock0:unmanaged"'
    # A drop-in read after Flock's that sets unmanaged-devices= replaces its list: status says so.
    root 'printf "[keyfile]\nunmanaged-devices=interface-name:eth*;interface-name:en*\n" > /etc/NetworkManager/conf.d/zzz-admin.conf && systemctl restart NetworkManager && sleep 5'
    check "a drop-in after Flock's: status reports NetworkManager managing flock0 within 70 s" status_says_nm 70
    root 'rm /etc/NetworkManager/conf.d/zzz-admin.conf && systemctl restart NetworkManager && systemctl restart flock-dns.service && sleep 4'
    check "drop-in removed: flock0 unmanaged again" is_unmanaged
    check "status clean again" status_says no 70
  fi

  echo "== drift"
  check "status clean" status_says no 5
  root 'ip address flush dev flock0'
  check "status reports a flushed flock0 within 70 s" status_says yes 70
  # Older systemd-resolved keeps the stale scope: the drift must show while *.test still works.
  echo "      *.test while it's reported: $(root 'getent hosts hello.test >/dev/null && echo still resolves || echo fails')"
  root 'systemctl restart flock-dns.service'
  check "restarting flock-dns.service clears it within 70 s" status_says no 70
  resolves "repaired"

  echo "== resolved restart, reboot"
  root 'systemctl restart systemd-resolved'
  sleep 3
  resolves "resolved restarted"
  podman restart -t 10 "$C" >/dev/null
  boot
  sleep 5
  check "after reboot: flock-dns.service and the user unit active" user 'systemctl --user is-active flock && systemctl is-active flock-dns.service'
  resolves "rebooted"

  echo "== uninstall --purge"
  user '$F uninstall --purge </dev/null >/dev/null 2>&1'
  check "flock-dns.service and flock0 gone" root '! test -e /etc/systemd/system/flock-dns.service && ! ip link show flock0 2>/dev/null'
  check "NetworkManager drop-in gone" root '! ls /etc/NetworkManager/conf.d/*flock* 2>/dev/null | grep -q .'
  if $nm; then
    check "other NetworkManager drop-ins kept" root 'test -f /etc/NetworkManager/conf.d/uplink.conf'
  fi
  check "CA gone from the system and NSS" user '! certutil -d sql:$HOME/.pki/nssdb -L | grep -qi flock && ! ls /usr/local/share/ca-certificates/flock.crt /etc/pki/ca-trust/source/anchors/flock.crt /etc/ca-certificates/trust-source/anchors/flock.crt 2>/dev/null | grep -q .'
  check "PATH line, user unit and ~/.config/Flock gone" user '! grep -q Flock/bin ~/.bashrc && ! test -e ~/.config/systemd/user/flock.service && ! test -e ~/.config/Flock'
  check "hello.test gone, example.com resolves" root '! getent hosts hello.test && getent hosts example.com'

  echo "== $d: $fails failed"
  total=$((total + fails))
  podman rm -f "$C" >/dev/null
done
echo "== $total failed in all"
exit $((total > 0))
