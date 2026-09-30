#!/bin/sh
sleep 30 &
echo $! > server.pid
echo "started"
