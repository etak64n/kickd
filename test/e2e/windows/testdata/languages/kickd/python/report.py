import os
import sys

env = os.environ
print("event=" + env["KICKD_EVENT"])
print("trigger=" + env["KICKD_TRIGGER"])
print("msg=" + env["KICKD_DATA_MSG"])
print("dir=" + os.getcwd())
with open(env["KICKD_PAYLOAD_FILE"], encoding="utf-8") as f:
    print("payload=" + f.read())
print("to stderr", file=sys.stderr)
sys.exit(int(env["KICKD_DATA_CODE"]))
