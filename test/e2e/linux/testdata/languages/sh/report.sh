#!/bin/sh
echo "event=$KICKD_EVENT"
echo "trigger=$KICKD_TRIGGER"
echo "msg=$KICKD_DATA_MSG"
echo "dir=$(pwd -P)"
echo "payload=$(cat "$KICKD_PAYLOAD_FILE")"
echo "to stderr" >&2
exit "$KICKD_DATA_CODE"
