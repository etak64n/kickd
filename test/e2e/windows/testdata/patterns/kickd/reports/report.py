import sys

print(f"venv={sys.prefix != sys.base_prefix}")
print(f"prefix={sys.prefix}")
