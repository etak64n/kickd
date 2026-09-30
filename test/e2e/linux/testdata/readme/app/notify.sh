#!/bin/sh
# Sends a notification. This one reports what kickd passed it.
echo "event=$KICKD_EVENT"
echo "trigger=$KICKD_TRIGGER"
echo "after=$KICKD_AFTER_EVENT"
echo "status=$KICKD_AFTER_STATUS"
echo "dir=$(pwd -P)"
