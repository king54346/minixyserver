"""Derive a price-only release from the exact deployed binaries.

The old per-server source snapshots were removed during workspace cleanup.
Patch only the two verified inlined xlIgnorePrice branches, preserving all
other per-server behavior. Every output must pass the external protocol test.
"""
from pathlib import Path
import hashlib
import json
import struct
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT/'bin/material-price-20261101'
OUT.mkdir(parents=True, exist_ok=True)
BASE = {
    'first': '3d8c0483dd86d4e650d22da46b9a9a4cb1634b1a233afc09e9bb84519d651cd1',
    'second': '14fbddfedf72bf8fe9f1a546bdd4be95262ac7b893196c20c4a567fc3e4596fe',
}
PATTERNS = [
    ('onXLIgnore', bytes.fromhex('48 83 fa 03 b9 58 02 00 00 ba 64 00 00 00 0f 4e ca'), 10),
    ('onXLExchange', bytes.fromhex('48 83 fa 03 ba 58 02 00 00 41 bc 64 00 00 00 41 0f 4e d4'), 11),
]


def virtual_address(data, offset):
    assert data[:6] == b'\x7fELF\x02\x01' and struct.unpack_from('<H', data, 18)[0] == 62
    phoff = struct.unpack_from('<Q', data, 32)[0]
    entsize, count = struct.unpack_from('<HH', data, 54)
    for i in range(count):
        kind, flags, fileoff, addr, _, size, _, _ = struct.unpack_from('<IIQQQQQQ', data, phoff+i*entsize)
        if kind == 1 and flags & 1 and fileoff <= offset < fileoff+size:
            return addr+offset-fileoff
    raise AssertionError('price location outside executable ELF segment')


for server, expected in BASE.items():
    source = ROOT/'bin'/('xymserver-xianling-38ad-'+server+'-linux')
    original = source.read_bytes()
    assert hashlib.sha256(original).hexdigest() == expected
    patched = bytearray(original)
    events = []
    for function, pattern, low in PATTERNS:
        assert original.count(pattern) == 1, function
        offset = original.index(pattern)
        address = virtual_address(original, offset)
        symbol = 'turnrpg/internal/xymsrv.'+function
        assembly = subprocess.check_output(['objdump', '-d', '--disassemble='+symbol, str(source)]).decode()
        assert ('%x:' % address) in assembly and ('<'+symbol+'>:') in assembly
        (OUT/(server+'-'+function+'-before.asm')).write_text(assembly, encoding='utf-8')
        for relative, old, new in [(5, 600, 300), (low, 100, 50)]:
            pos = offset+relative
            assert struct.unpack_from('<I', original, pos)[0] == old
            struct.pack_into('<I', patched, pos, new)
            events.append(dict(function=function, file_offset=pos, old=old, new=new))
    changed = [i for i, (old, new) in enumerate(zip(original, patched)) if old != new]
    assert len(original) == len(patched) and len(changed) == 6
    target = OUT/('xymserver-material-'+server+'-linux')
    target.write_bytes(patched)
    for function, _, _ in PATTERNS:
        assembly = subprocess.check_output(['objdump', '-d', '--disassemble=turnrpg/internal/xymsrv.'+function, str(target)]).decode()
        (OUT/(server+'-'+function+'-after.asm')).write_text(assembly, encoding='utf-8')
        assert '$0x12c,' in assembly and '$0x32,' in assembly
    report = dict(server=server, original_sha256=expected,
                  sha256=hashlib.sha256(patched).hexdigest(), bytes_changed=changed,
                  edits=events, requires_external_protocol_test=True)
    (OUT/(server+'-patch.json')).write_text(json.dumps(report, indent=2), encoding='utf-8')
    print(json.dumps(report))
