"""Headless end-to-end checks with a fake admin API; no production access."""
import copy
import json
import os
from pathlib import Path
import threading
from urllib.parse import parse_qs, urlparse
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from playwright.sync_api import sync_playwright
import gm


def main():
    drops = {
        'recruit': {str(i): {'weights': {'3': 790, '4': 200, '5': 10}, 'pity': 50,
                            'ten_min': 4, 'rare_bp': 30} for i in range(1, 6)},
        'boxes': {str(i): {'again_bp': 200, 'drops': [
            {'code': 'GPSP5', 'count': 1, 'weight': 20},
            {'code': 'GPFS5', 'count': 1, 'weight': 1}]} for i in [10002, 10004, 10006]},
    }
    rule = dict(id='seven_day_500k', name='全服七天福利', enabled=False, mode='eligible',
                time='', start_date='2026-11-02', end_date='2026-11-08',
                require_login=True, min_active=125, online_only=False, new_player_days=0,
                reward=dict(title='七天福利', text='测试', gold=500000, cash=0, soul=0, items={}))
    state = dict(version=1, drops=drops, rules=[])
    calls = []

    def fake_admin(method, path, body=None):
        calls.append((method, path, copy.deepcopy(body)))
        if path == '/players':
            return 200, [dict(uid=20001, name='测试角色', grade=50, online=True),
                         dict(uid=20002, name='在线角色', grade=40, online=True),
                         dict(uid=20003, name='离线角色', grade=30, online=False)]
        if path == '/mail':
            return 200, {'sent': len(body.get('uids', [body['to']]))}
        if path == '/kick':
            return 200, {'ok': 'kicked'}
        if path == '/player?uid=20001':
            return 200, dict(role=dict(uid=20001, name='测试角色'))
        if path.startswith('/records/player?uid=20001'):
            return 200, dict(day='20261103', total=1, rows=[
                dict(file='20261103/120000_20001_1.rec', time='120000', opponent='测试对手', result='胜利', rid='rec-1')])
        if path.startswith('/records/detail?uid=20001'):
            return 200, dict(fight=dict(name1='测试角色', name2='测试对手', rid='rec-1',
                                       level1=50, level2=50, team1=[], team2=[]),
                             commands=[dict(id=4, actor=0, magic=21794, name='诸佛消散', targets=[[102,150]])],
                             total_commands=1)
        if path in ('/accounts', '/catalog', '/dongfus'):
            return 200, []
        if path == '/gm/operations':
            return 200, dict(settings=copy.deepcopy(state), seven_day_template=rule,
                             conservative_drops=drops, base_drops=drops,
                             scheduler_running=True, server_time='2026-11-01T12:00:00+08:00')
        if path == '/gm/settings':
            assert body['version'] == state['version']
            state.update(copy.deepcopy(body)); state['version'] += 1
            return 200, state
        if path.startswith('/gm/logs?'):
            return 200, dict(file='/data/gm-ops-2026-11-01.jsonl', events=[
                dict(kind='mail_claim', uid=20001, name='测试角色', day='2026-11-01',
                     data=[dict(grant_day='2026-10-31', gold=500000)])])
        if path == '/gm/preview':
            return 200, dict(rows=[dict(uid=20001, name='测试角色', reason='符合条件',
                                       login=True, active=125, online=False)], dry_run=True)
        if path == '/box':
            return 200, {10002: dict(soulMin=20, soulMax=23, drops=[
                dict(w=1, code='ZFBOOT', name='金鞋', cnt=1),
                dict(w=63, code='WP10015', name='高级经验丹', cnt=1, gradeOnly=[30, 40]),
            ])}
        if path in ('/box_set', '/box_reset'):
            return 200, dict(ok=True, count=2)
        if path == '/broadcast':
            return 200, dict(sent=3, ok=True)
        return 200, {}

    gm.admin = fake_admin
    server = gm.ThreadingHTTPServer(('127.0.0.1', 0), gm.Handler)
    gm.PORT = server.server_port
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{gm.PORT}'
    errors = []
    try:
        with sync_playwright() as pw:
            browsers = sorted((Path(os.environ['LOCALAPPDATA'])/'ms-playwright').glob('chromium-*/chrome-win*/chrome.exe'))
            browser = pw.chromium.launch(headless=True, executable_path=str(browsers[-1]) if browsers else None)
            page = browser.new_page(viewport={'width': 1440, 'height': 1080})
            page.on('pageerror', lambda e: errors.append(str(e)))
            page.goto(base+'/')
            page.locator('#btnRefresh').click()
            page.wait_for_function("document.querySelector('#onlineCount').textContent.includes('2 / 3')")
            page.get_by_role('checkbox', name='勾选 在线角色 (20002)').check()
            page.locator('#btnKick').click()
            page.get_by_text('uid 20002 已踢下线', exact=False).wait_for()
            assert [c[2] for c in calls if c[1] == '/kick'] == [{'uid': 20002}]
            page.get_by_text('测试角色', exact=True).click()
            page.locator('#roleRecords').wait_for()
            page.locator('#btnKick').click()
            page.wait_for_function("document.querySelector('#log').textContent.match(/uid 20002 已踢下线/g)?.length === 2")
            assert [c[2] for c in calls if c[1] == '/kick'] == [{'uid': 20002}, {'uid': 20002}]
            page.locator('#onlineFilter').select_option('online')
            assert page.locator('#players tbody tr').count() == 2
            page.locator('#checkVisible').check()
            page.locator('#btnKick').click()
            page.get_by_text('踢下线一次只能处理一名玩家', exact=False).wait_for()
            assert len([c for c in calls if c[1] == '/kick']) == 2
            page.locator('#onlineFilter').select_option('offline')
            assert page.locator('#players tbody tr').count() == 1
            assert page.locator('#checkedCount').inner_text() == '已勾选 2 人'
            page.locator('#mailGold').fill('5')
            page.once('dialog', lambda d: d.accept())
            page.locator('#btnMail').click()
            page.wait_for_function("document.querySelector('#log').textContent.includes('已发送 2 封')")
            sent = [c[2] for c in calls if c[1] == '/mail'][-1]
            assert sent['to'] == -1 and sorted(sent['uids']) == [20001, 20002]
            page.locator('#clearChecked').click()
            page.locator('#btnKick').click()
            page.get_by_text('uid 20001 已踢下线', exact=False).wait_for()
            assert [c[2] for c in calls if c[1] == '/kick'][-1] == {'uid': 20001}
            page.goto(base+'/operations.html')
            page.wait_for_function("document.querySelector('#status').textContent.includes('已连接')")
            page.locator('#seven').click()
            assert page.locator('#gold').input_value() == '500000'
            assert page.locator('#needLogin').is_checked() and page.locator('#needActive').is_checked()
            for login, active, online in [(False, False, False), (True, False, False),
                                          (False, True, False), (True, True, True)]:
                page.locator('#needLogin').set_checked(login)
                page.locator('#needActive').set_checked(active)
                page.locator('#onlineOnly').set_checked(online)
                page.locator('#uids').fill('20001 20002,20003')
                page.locator('#preview').click()
                page.wait_for_timeout(80)
                body = [c[2] for c in calls if c[1] == '/gm/preview'][-1]
                assert (body['require_login'], body['min_active'], body['online_only']) == (login, 125 if active else 0, online)
                assert body['uids'] == [20001, 20002, 20003]
                assert '20001 测试角色' in page.locator('#previewResult').inner_text()
            page.locator('#saveRule').click()
            page.wait_for_function("document.querySelector('#status').textContent.includes('规则已保存')")
            assert len(state['rules']) == 1 and not state['rules'][0]['enabled']
            page.locator('#lowPreset').click()
            page.locator('#saveDrops').click()
            page.wait_for_function("document.querySelector('#status').textContent.includes('概率已保存')")
            assert state['drops']['recruit']['1']['pity'] == 50
            page.locator('#logUID').fill('20001'); page.locator('#logKind').select_option('mail_claim')
            page.locator('#logs').click()
            page.wait_for_function("document.querySelector('#logResult').textContent.includes('测试角色')")
            query = parse_qs(urlparse(calls[-1][1]).query)
            assert query['uid'] == ['20001'] and query['kind'] == ['mail_claim']
            output = Path(__file__).resolve().parents[1]/'server/bin/perf-20261030'
            page.screenshot(path=str(output/'gm-operations-ui.png'), full_page=True)
            page.goto(base+'/broadcast.html')
            assert page.locator('body').inner_text().find('播报') >= 0
            for name in ('index', 'accounts', 'pets', 'dongfu', 'xiuwei', 'progress', 'bag'):
                response = page.goto(base+'/'+name+'.html')
                assert response.status == 200, name
                page.wait_for_timeout(120)
            assert page.locator('body').inner_text().find('背包') >= 0 or page.locator('body').inner_text().find('物品') >= 0
            assert not errors, errors
            page.goto(base+'/box.html')
            page.get_by_text('仅 30 / 40 级', exact=True).wait_for()
            field = page.locator('input.w').first
            field.fill('')
            field.press_sequentially('123')
            assert field.input_value() == '123'
            page.locator('#btnSave').click()
            page.wait_for_function("document.getElementById('saveTip').textContent.includes('已保存')")
            assert any(m == 'POST' and p == '/box_set' and b['drops'][0]['w'] == 123 for m, p, b in calls)
            page.once('dialog', lambda dialog: dialog.accept())
            page.locator('#btnReset').click()
            page.wait_for_function("document.getElementById('saveTip').textContent.includes('已恢复')")
            assert any(m == 'POST' and p == '/box_reset' and b['box'] == 10002 for m, p, b in calls)
            page.goto(base+'/')
            page.locator('#btnRefresh').click()
            page.get_by_text('测试角色', exact=True).click()
            link = page.locator('#roleRecords')
            link.wait_for()
            assert link.get_attribute('href') == '/records.html?uid=20001'
            page.goto(base + link.get_attribute('href'))
            page.get_by_text('测试对手', exact=True).wait_for()
            page.get_by_role('button', name='查看', exact=True).click()
            page.get_by_text('扣血 诸佛消散', exact=False).wait_for()
            assert not errors, errors
            browser.close()
        req = Request(base+'/api/settings', data=b'{}', headers={'Origin': 'http://untrusted.invalid'}, method='POST')
        try:
            urlopen(req)
            raise AssertionError('cross-origin POST accepted')
        except HTTPError as err:
            assert err.code == 403
        print('PASS: GM conditions, seven-day template, UID parsing, persisted settings, odds, audit filters, broadcast page and origin guard')
    finally:
        server.shutdown(); server.server_close()


if __name__ == '__main__':
    main()
