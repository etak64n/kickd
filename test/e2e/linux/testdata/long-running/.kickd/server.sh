#!/bin/sh
# Ignores SIGTERM and runs until stop.sh leaves the file stop.
trap '' TERM
rm -f stop
echo $$ > server.pid
echo "$GREETING from $KICKD_TRIGGER"
while [ ! -f stop ]; do sleep 0.1; done
echo "stopped by $(cat stop)"
rm -f stop
