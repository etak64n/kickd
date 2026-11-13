#!/bin/sh
# Notes the attempt that the payload file holds, and the file, then syncs.
attempt=$(grep -o '"attempt":[0-9]*' "$KICKD_PAYLOAD_FILE" | cut -d : -f 2)
echo "$attempt $KICKD_PAYLOAD_FILE" >> payloads.txt
sleep 4
echo "synced"
