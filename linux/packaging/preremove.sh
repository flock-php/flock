#!/bin/sh
# Before removing the package (not before an upgrade): every user's daemon is stopped and their
# system-wide setup undone (unit drop-in, *.test DNS, CA trust, Firefox policy) by the flock that
# set it up, `privileged-setup --remove`. Users' own files (data dir, shell PATH line, browsers'
# certificate databases) stay: `flock uninstall` removes those.
set -e
case "$1" in
  remove | 0) ;;
  *) exit 0 ;;
esac
for dir in /etc/systemd/system/flock@?*.service.d; do
  [ -d "$dir" ] || continue
  user="${dir#/etc/systemd/system/flock@}"
  user="${user%.service.d}"
  /usr/libexec/flockphp/flock privileged-setup --remove --user "$user" || echo "flockphp: could not undo Flock's setup for $user" >&2
done
