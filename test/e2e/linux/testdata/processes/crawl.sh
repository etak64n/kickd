#!/bin/sh
sh -c 'echo $$ > crawler.pid; exec sleep 60' &
wait
