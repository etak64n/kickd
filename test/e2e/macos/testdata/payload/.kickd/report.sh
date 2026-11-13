#!/bin/sh
# Reports the payload file of the run, and its permissions.
echo "file=$KICKD_PAYLOAD_FILE"
echo "mode=$(ls -l "$KICKD_PAYLOAD_FILE" | cut -c 1-10)"
