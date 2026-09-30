#!/bin/sh
i=1
while [ "$i" -le 200 ]; do
  echo "host $i answers"
  echo "host $i is slow" >&2
  i=$((i + 1))
done
