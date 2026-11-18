"""Patch the active battle status translator; preserve every other script."""
from pathlib import Path
import argparse
import hashlib

ROOT = Path(__file__).resolve().parents[2]
RELATIVE = Path('logic/pet_mgr.lua')
ORIGINAL = ROOT / 'client/jadx-out/resources/assets/script'
TARGET = ROOT / 'client2/apk-src/assets/script'


def patched_detail(data):
    text = data.decode('utf-8')
    newline = '\r\n' if '\r\n' in text else '\n'
    changes = [
        ('\tlocal buffFlag = {}', '\tlocal buffFlag = {}\n\tlocal burnCount = {}\n\tfor _,b in pairs(warPetInfo.buffs or {}) do\n\t\tif b and b.mid>=5021 and b.mid<=5030 then burnCount[b.mid]=(burnCount[b.mid] or 0)+1 end\n\tend'),
        ('buff and buffFlag[buff.mid]==nil and buff_mgr.createInfo(buff.mid)', 'buff and (buffFlag[buff.mid]==nil or burnCount[buff.mid]) and buff_mgr.createInfo(buff.mid)'),
        ('buffFlag[buff.mid]=true', 'buffFlag[buff.mid]=(buffFlag[buff.mid] or 0)+1\n\t\t\t\t\tlocal buffName=LOCAL_TXT(buff_mgr.createInfo(buff.mid).name)\n\t\t\t\t\tif burnCount[buff.mid] and burnCount[buff.mid]>1 then buffName=buffName.."（第"..buffFlag[buff.mid].."层）" end'),
        ('bdesc=bdesc..LOCAL_TXT(buff_mgr.createInfo(buff.mid).name)..": "', 'bdesc=bdesc..buffName..": "'),
    ]
    for old, new in changes:
        new = new.replace('\n', newline)
        assert text.count(old) == 1, f'original translator does not match: {old}'
        text = text.replace(old, new)
    return text.encode('utf-8')


def check_all():
    originals = {p.relative_to(ORIGINAL): p for p in ORIGINAL.rglob('*') if p.is_file()}
    targets = {p.relative_to(TARGET): p for p in TARGET.rglob('*') if p.is_file()}
    assert originals.keys() == targets.keys(), 'script file list changed'
    for relative, path in originals.items():
        data = path.read_bytes()
        expected = patched_detail(data) if relative == RELATIVE else data
        assert targets[relative].read_bytes() == expected, f'unexpected script edit: {relative}'
    print(f'PASS: {len(originals)-1} original scripts unchanged; only active pet_mgr battle status translation changed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--check-all', action='store_true')
    args = parser.parse_args()
    expected = patched_detail((ORIGINAL / RELATIVE).read_bytes())
    path = TARGET / RELATIVE
    if args.apply:
        assert path.read_bytes() in ((ORIGINAL / RELATIVE).read_bytes(), expected)
        path.unlink()  # Previous APK staging trees may share a hard link.
        path.write_bytes(expected)
    if args.check_all:
        check_all()
    else:
        assert path.read_bytes() == expected
        print('Patched pet_mgr SHA-256:', hashlib.sha256(expected).hexdigest())
