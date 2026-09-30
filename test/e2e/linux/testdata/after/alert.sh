#!/bin/sh
# Reports the run that ended.
echo "trigger=$KICKD_TRIGGER"
echo "after=$KICKD_AFTER_EVENT"
echo "run=$KICKD_AFTER_RUN_ID"
echo "status=$KICKD_AFTER_STATUS"
echo "exit=$KICKD_AFTER_EXIT_CODE"
