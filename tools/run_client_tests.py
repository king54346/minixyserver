"""Run the maintained Lua 5.1 regressions from the server working directory."""
import argparse
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--lua', help='Lua 5.1 interpreter path')
args = parser.parse_args()
bundled = ROOT.parent/'client/tools/lua51.exe'
lua = args.lua or (str(bundled) if bundled.exists() else shutil.which('lua5.1'))
if not lua:
    parser.error('Lua 5.1 was not found; specify --lua')
tests = sorted((ROOT/'tools/tests').glob('test_*.lua'))
if not tests:
    raise SystemExit('No client regressions found')
for test in tests:
    subprocess.run([lua, str(test)], cwd=ROOT, check=True, timeout=120)
print('PASS: %d client regression files' % len(tests))
