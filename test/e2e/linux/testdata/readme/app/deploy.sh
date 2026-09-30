#!/bin/sh
# Deploys the app. This one reports what kickd passed it.
echo "event=$KICKD_EVENT"
echo "trigger=$KICKD_TRIGGER"
echo "dir=$(pwd -P)"
