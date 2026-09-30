#!/bin/sh
sleep 30 >server.log 2>&1 &
echo $! > server.pid
echo "started"
