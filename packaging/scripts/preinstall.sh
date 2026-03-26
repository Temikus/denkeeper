#!/bin/sh
# Create a dedicated system user for denkeeper if it doesn't already exist.
if ! id denkeeper >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /sbin/nologin --home-dir /var/lib/denkeeper denkeeper
fi
