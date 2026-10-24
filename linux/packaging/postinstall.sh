#!/bin/sh
# After installing or upgrading: systemd learns about flock@.service, and apt or dnf about Flock's
# repository (stable, unless `flock channel` chose beta), so Flock updates with the system. Setting
# Flock up is per user (`flk install`, or the app). An upgrade restarts every running daemon, which
# runs the new flock through the link `flk install` put in the user's data dir.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
/usr/lib/flockphp/flock privileged-setup --repo stable --if-missing || echo "flockphp: could not add Flock's package repository" >&2
# deb: configure <old version> on an upgrade; rpm: the number of installed versions (2 while upgrading).
if [ "$1" = configure ] && [ -n "$2" ] || [ "$1" = 2 ]; then
  if [ -d /run/systemd/system ]; then
    systemctl try-restart 'flock@*.service' || true
  fi
elif [ "$1" = configure ] || [ "$1" = 1 ]; then
  echo "Flock is installed. Set it up for your user with: flk install"
fi
