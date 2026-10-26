#!/bin/sh
# After the package is purged (deb) or erased (rpm, not upgraded): Flock's repository goes too. A
# plain `apt remove` keeps it, like the package's other configuration.
set -e
case "$1" in
  purge | 0) rm -f /etc/apt/sources.list.d/flockphp.sources /etc/yum.repos.d/flockphp.repo ;;
esac
