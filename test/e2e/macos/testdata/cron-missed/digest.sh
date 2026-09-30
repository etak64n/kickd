#!/bin/sh
# Reports the time that the schedule fired for.
echo "trigger=$KICKD_TRIGGER"
echo "scheduled=$KICKD_CRON_SCHEDULED_AT"
echo "missed=$KICKD_CRON_MISSED"
