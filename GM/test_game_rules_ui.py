"""Exercise gameplay settings against a local fake admin, without SSH access."""
import copy
import os
from pathlib import Path
import threading
from playwright.sync_api import sync_playwright
import gm

def main():
    state = {'version': 1, 'changes': {}}
    registration = {'version': 1, 'disabled': False}
    calls = []
    def admin(method, path, body=None):
        calls.append((method, path, copy.deepcopy(body)))
        if path == '/gm/registration':
            if method == 'POST':
                if body['version'] != registration['version']:
                    return 409, {'error': '注册配置已被其他窗口修改，请重新读取'}
                registration.update(copy.deepcopy(body)); registration['version'] += 1
            return 200, copy.deepcopy(registration)
        if method == 'POST':
            if body['version'] != state['version']:
                return 409, {'error': '配置已被其他窗口修改，请重新读取'}
            state.update(copy.deepcopy(body)); state['version'] += 1
            return 200, {'settings': copy.deepcopy(state), 'restart_required': True}
        return 200, {'settings': copy.deepcopy(state), 'active': {'version': 1, 'changes': {}},
                     'groups': {'counts': 1, 'shop': 1}, 'offset': 0, 'total': 1,
                     'restart_required': bool(state['changes']),
                     'rows': [{'key': 'shop/11153', 'group': 'shop', 'name': '无字天书',
                               'values': {'Price': 500, 'Limit': 0}}]}
    gm.admin = admin
    srv = gm.ThreadingHTTPServer(('127.0.0.1', 0), gm.Handler)
    gm.PORT = srv.server_port
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    errors = []
    try:
        with sync_playwright() as pw:
            candidates = sorted((Path(os.environ['LOCALAPPDATA'])/'ms-playwright').glob('chromium-*/chrome-win*/chrome.exe'))
            browser = pw.chromium.launch(headless=True, executable_path=str(candidates[-1]) if candidates else None)
            page = browser.new_page(viewport={'width': 1440, 'height': 1000})
            page.on('pageerror', lambda error: errors.append(str(error)))
            page.goto(f'http://127.0.0.1:{srv.server_port}/game-rules.html')
            page.locator('input[data-field="Price"]').wait_for()
            page.wait_for_function("!document.querySelector('#registrationSave').disabled")
            page.locator('#registrationDisabled').check()
            page.locator('#registrationSave').click()
            page.wait_for_function("document.querySelector('#registrationStatus').textContent.includes('已立即禁止')")
            assert registration['disabled']
            assert not state['changes'], 'registration save changed gameplay rules'
            registration['version'] += 1
            page.locator('#registrationDisabled').uncheck()
            page.locator('#registrationSave').click()
            page.wait_for_function("document.querySelector('#registrationStatus').textContent.includes('其他窗口')")
            assert registration['disabled'], 'stale page reopened registration'
            page.locator('#registrationReload').click()
            page.wait_for_function("document.querySelector('#registrationDisabled').checked")
            page.locator('input[data-field="Price"]').fill('125')
            page.locator('input[data-field="Price"]').blur()
            page.locator('#save').click()
            page.wait_for_function("document.querySelector('#message').textContent.includes('保存成功')")
            assert state['changes']['shop/11153']['Price'] == 125
            state['version'] += 1
            page.locator('#save').click()
            page.wait_for_function("document.querySelector('#message').textContent.includes('其他窗口')")
            page.on('dialog', lambda d: d.accept())
            page.locator('#reload').click()
            page.wait_for_function(f"document.querySelector('#message').textContent.includes('版本 {state['version']}')")
            page.screenshot(path=str(Path(__file__).with_name('game-rules-preview.png')), full_page=True)
            assert not errors, errors
            assert any(path.startswith('/gm/game-rules?') for _, path, _ in calls)
            browser.close()
        print('PASS: gameplay settings edit/save/persistence/stale version/reload')
    finally:
        srv.shutdown(); srv.server_close()

if __name__ == '__main__':
    main()
