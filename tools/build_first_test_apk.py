"""Build the maintained client for either server; remove this build's staging files."""
from pathlib import Path
import argparse
import os
import shutil
import subprocess
import tempfile
import zipfile

root = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser()
parser.add_argument('--server', choices=['first', 'second'], default='first')
parser.add_argument('--output', default='迷你西游-一服整理版-20261031.apk')
args = parser.parse_args()
assert Path(args.output).name == args.output and args.output.endswith('.apk')
source = root / 'client2/apk-src'
out = root / 'client2/out'
out.mkdir(exist_ok=True)
subprocess.run(['python', str(root/'server/tools/patch_battle_detail.py'), '--check-all'], check=True)
subprocess.run(['python', str(root/'server/tools/sync_client_fixes.py')], check=True)
prefix = args.server + '-server-build-'
stage = Path(tempfile.mkdtemp(prefix=prefix, dir=out)).resolve()
assert stage.parent == out.resolve() and stage.name.startswith(prefix)
work = stage / 'src'
try:
    shutil.copytree(source, work, copy_function=os.link, ignore=shutil.ignore_patterns('build', 'dist'))
    p = work/'assets/lib/gamemain.lua'
    text = p.read_text(encoding='utf-8')
    old = '{"再续前缘", "117.72.217.131", 60010}'
    assert text.count(old) == 1
    endpoint = '{"一服测试", "121.37.152.248", 8111}' if args.server == 'first' else old
    text = text.replace(old, endpoint)
    p.unlink()  # Break the hard link before changing the test-only endpoint.
    p.write_text(text, encoding='utf-8', newline='')
    bt = root.parent/'android/build-tools/36.0.0'
    unsigned, aligned = stage/'unsigned.apk', stage/'aligned.apk'
    apk = out/args.output
    subprocess.run(['java', '-jar', str(root/'client/tools/apktool.jar'), 'b', str(work), '-o', str(unsigned)], check=True)
    subprocess.run([str(bt/'zipalign.exe'), '-f', '-p', '4', str(unsigned), str(aligned)], check=True)
    subprocess.run([str(bt/'apksigner.bat'), 'sign', '--ks', str(root/'client/tools/debug.keystore'), '--ks-pass', 'pass:android', '--out', str(apk), str(aligned)], check=True)
    subprocess.run([str(bt/'apksigner.bat'), 'verify', '--verbose', str(apk)], check=True)
    subprocess.run([str(bt/'zipalign.exe'), '-c', '-p', '4', str(apk)], check=True)
    with zipfile.ZipFile(apk) as z:
        assert z.read('assets/lib/gamemain.lua').decode('utf-8') == text
        assert z.read('assets/lib/local_fixes.lua') == (source/'assets/lib/local_fixes.lua').read_bytes()
    print(apk)
finally:
    # Only the directory created above; never remove source or other build trees.
    assert stage.parent == out.resolve() and stage.name.startswith(prefix)
    shutil.rmtree(stage)
