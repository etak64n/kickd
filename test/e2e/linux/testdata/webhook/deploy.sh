#!/bin/sh
# Reports the request that fired the event.
echo "trigger=$KICKD_TRIGGER"
echo "request=$KICKD_REQUEST_ID"
echo "body=$(cat "$KICKD_PAYLOAD_FILE")"
