"""Repack an existing iOS client with maintained client2 Lua fixes; requires re-signing."""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import plistlib
import subprocess
import zipfile

from patch_battle_detail import patched_detail
from sync_client_fixes import generate

ROOT = Path(__file__).resolve().parents[2]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('input', type=Path)
    parser.add_argument('--stage', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--server', choices=['original', 'first', 'second', 'both'], default='original')
    parser.add_argument('--bundle-id', help='Separate installation and data container from the original app')
    parser.add_argument('--display-name')
    args = parser.parse_args()
    source, stage, output = args.input.resolve(), args.stage.resolve(), args.output.resolve()
    assert not stage.exists(), 'Use a new staging directory'
    assert not output.exists() and source != output, 'Never overwrite an IPA'
    stage.mkdir(parents=True)
    changed = {}
    with zipfile.ZipFile(source) as original:
        infos = original.infolist()
        assert len({i.filename for i in infos}) == len(infos), 'Duplicate ZIP entries'
        roots = [i.filename[:-len('Info.plist')] for i in infos
                 if i.filename.startswith('Payload/') and i.filename.count('/') == 2
                 and i.filename.endswith('.app/Info.plist')]
        assert len(roots) == 1, roots
        app = roots[0]
        for info in infos:
            dest = (stage / info.filename).resolve()
            assert dest.is_relative_to(stage), 'Unsafe archive path'
            assert (info.external_attr >> 16) & 0o170000 != 0o120000, 'Symlinks need separate handling'
        original.extractall(stage)

        def replace(relative, data):
            name = app + relative
            dest = stage / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(data)
            changed[name] = data

        if args.bundle_id or args.display_name:
            data = original.read(app+'Info.plist')
            metadata = plistlib.loads(data)
            if args.bundle_id:
                metadata['CFBundleIdentifier'] = args.bundle_id
            if args.display_name:
                metadata['CFBundleDisplayName'] = metadata['CFBundleName'] = args.display_name
            replace('Info.plist', plistlib.dumps(metadata, fmt=plistlib.FMT_BINARY if data.startswith(b'bplist') else plistlib.FMT_XML, sort_keys=False))

        replace('lib/local_fixes.lua', generate().encode('utf-8'))
        replace('lib/reconnect_fixes.lua', (ROOT/'client2/apk-src/assets/lib/reconnect_fixes.lua').read_bytes())
        replace('script/logic/pet_mgr.lua', patched_detail(original.read(app+'script/logic/pet_mgr.lua')))
        if args.server != 'original':
            # This shared source retains OFFLINE_IOS branches; native iOS modules remain untouched.
            probe = (ROOT/'client2/apk-src/assets/script/offline_probe.lua').read_bytes()
            if args.server == 'second':
                old = b'M.SERVERS = _G.OFFLINE_SERVERS or {{M.ZONE, host, port}}'
                assert probe.count(old) == 1
                # A standalone second-server package must not accept stale global endpoints.
                probe = probe.replace(old, 'M.SERVERS = {{"再续前缘", "117.72.217.131", 60010}}'.encode('utf-8'))
            replace('script/offline_probe.lua', probe)
        servers = {
            'first': [("一服", "121.37.152.248", 8111)],
            'second': [("再续前缘", "117.72.217.131", 60010)],
            'both': [("再续前缘", "117.72.217.131", 60010), ("一服", "121.37.152.248", 8111)],
        }
        for relative in ['lib/gamemain.lua', 'script/gamemain.lua']:
            data = original.read(app+relative)
            newline = b'\r\n' if b'\r\n' in data else b'\n'
            if args.server != 'original':
                lines = [b'\t_G.OFFLINE_IOS = true', b'\t_G.OFFLINE_ENTER_WORLD = true',
                         b'\t_G.OFFLINE_LOCAL_LOGIN = true', b'\t_G.OFFLINE_SERVERS = {']
                for name, host, port in servers[args.server]:
                    lines.append(('\t\t{"%s", "%s", %d},' % (name, host, port)).encode('utf-8'))
                lines += [b'\t}', b'\t_G.OFFLINE_HOST = _G.OFFLINE_SERVERS[1][2]',
                          b'\t_G.OFFLINE_PORT = _G.OFFLINE_SERVERS[1][3]']
                start = data.find(b'\t_G.OFFLINE_IOS = true')
                end = data.find(b'\trequire("offline_probe").install()')
                if start >= 0:
                    assert end > start
                    data = data[:start] + newline.join(lines) + newline + data[end:]
                else:
                    anchor = b'\trequire "main"'
                    assert data.count(anchor) == 1
                    block = newline.join(lines + [b'\trequire("offline_probe").install()']) + newline
                    data = data.replace(anchor, block+anchor)
            anchor = b'\trequire "main"'
            assert data.count(anchor) == 1
            hooks = newline.join([b'\trequire("local_fixes")', b'\trequire("reconnect_fixes")']) + newline
            replace(relative, data.replace(anchor, hooks+anchor))

        luac = ROOT/'client/tools/luac51.exe'
        for name in changed:
            if name.endswith('.lua'):
                subprocess.run([str(luac), '-p', str(stage/name)], check=True)
        # Native executable and frameworks are preserved. The outer app's resource signature
        # cannot survive resource edits, so omit it instead of presenting a stale signature.
        removed = [i.filename for i in infos if i.filename.startswith(app+'_CodeSignature/')
                   or (args.bundle_id and i.filename == app+'embedded.mobileprovision')]
        output.parent.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as packed:
            packed.comment = original.comment
            for info in infos:
                if info.filename in removed:
                    continue
                packed.writestr(copy.copy(info), b'' if info.is_dir() else (stage/info.filename).read_bytes())
            for name, data in changed.items():
                if name not in original.namelist():
                    info = zipfile.ZipInfo(name)
                    info.create_system = 3
                    info.external_attr = 0o100644 << 16
                    info.compress_type = zipfile.ZIP_DEFLATED
                    packed.writestr(info, data)
        with zipfile.ZipFile(output) as packed:
            assert packed.testzip() is None
            for info in infos:
                if not info.is_dir() and info.filename not in removed:
                    expected = changed.get(info.filename)
                    if expected is None:
                        expected = original.read(info.filename)
                    assert packed.read(info.filename) == expected, info.filename
            for name, data in changed.items():
                assert packed.read(name) == data
            metadata = plistlib.loads(packed.read(app+'Info.plist'))
        manifest = {
            'input': str(source), 'input_sha256': sha(source.read_bytes()),
            'output': str(output), 'output_sha256': sha(output.read_bytes()),
            'server': args.server, 'requires_resigning': True,
            'bundle_id': metadata['CFBundleIdentifier'],
            'display_name': metadata.get('CFBundleDisplayName'),
            'changed': {name: sha(data) for name, data in changed.items()},
            'removed_invalid_signature': removed,
            'verification': 'ZIP CRC, every unchanged entry byte-identical, all changed Lua parses with Lua 5.1',
        }
        output.with_suffix('.manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
        print(json.dumps({'output': str(output), 'changed': len(changed), 'server': args.server,
                          'requires_resigning': True}, ensure_ascii=True))


if __name__ == '__main__':
    main()
