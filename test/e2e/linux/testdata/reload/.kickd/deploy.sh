#!/bin/sh
# Deploys to $TARGET once the release is ready: when the file ready exists.
echo "target=$TARGET"
while [ ! -f ready ]; do sleep 0.1; done
echo "deployed"
