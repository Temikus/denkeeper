#!/bin/sh
if systemctl is-active --quiet denkeeper; then
  systemctl stop denkeeper
fi
if systemctl is-enabled --quiet denkeeper 2>/dev/null; then
  systemctl disable denkeeper
fi
