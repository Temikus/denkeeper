#!/bin/sh
systemctl daemon-reload

echo ""
echo "Denkeeper installed. Next steps:"
echo "  1. sudo cp /etc/denkeeper/denkeeper.toml.example /etc/denkeeper/denkeeper.toml"
echo "  2. sudo \$EDITOR /etc/denkeeper/denkeeper.toml"
echo "  3. sudo systemctl enable --now denkeeper"
echo ""
echo "Logs: journalctl -u denkeeper -f"
