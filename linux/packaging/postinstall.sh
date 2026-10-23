#!/bin/sh
# After installing or upgrading: systemd learns about flock@.service. Setting Flock up is per user
# (`flk install`, or the app), so nothing else happens here.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
if [ "$1" = configure ] && [ -z "$2" ] || [ "$1" = 1 ]; then
  echo "Flock is installed. Set it up for your user with: flk install"
fi
