"""TLS fixture only: go test -tags browser -run TestDualExecutionBrowser ./..."""
import argparse
import json
from pathlib import Path
from playwright.sync_api import expect, sync_playwright

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--base', required=True)
parser.add_argument('--target', required=True)
args = parser.parse_args()
output = Path(__file__).resolve().parents[1] / 'docs/tasks/dual-execution/evidence'
output.mkdir(parents=True, exist_ok=True)

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, channel='chrome')
    context = browser.new_context(ignore_https_errors=True, viewport={'width': 1440, 'height': 1100})
    page = context.new_page()
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.goto(args.base, wait_until='networkidle')
    page.evaluate("navigator.clipboard.writeText = async text => { window.privateAgentInstructions = text }")
    results = []
    for mode in ('local_agent', 'platform'):
        page.get_by_role('button', name='新建实验', exact=True).first.click()
        page.get_by_role('button', name='OpenAI 兼容 API', exact=True).click()
        if mode == 'local_agent':
            page.get_by_role('button', name='本地 Agent · 可选', exact=True).click()
        else:
            page.get_by_role('button', name='平台代跑 · 推荐', exact=True).click()
        page.get_by_label('Endpoint', exact=True).fill(args.target)
        page.get_by_label('模型', exact=True).fill('browser-test-model')
        page.get_by_label('API Key', exact=True).fill('dual-fake-secret')
        page.get_by_label('并发阶梯', exact=True).fill('1')
        page.get_by_label('每阶段秒数', exact=True).fill('1')
        page.get_by_label('最大请求数', exact=True).fill('2')
        page.get_by_label('最大 RPS', exact=True).fill('5')
        page.get_by_role('checkbox').check()
        for width in (1440, 390, 320):
            page.set_viewport_size({'width': width, 'height': 1100})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), f'form overflow {width}'
            page.screenshot(path=str(output / f'{mode}-form-{width}.png'), full_page=True)
        page.set_viewport_size({'width': 1440, 'height': 1100})
        page.get_by_role('button', name='创建本地 Agent 任务' if mode == 'local_agent' else '授权并由平台运行', exact=True).click()
        if mode == 'local_agent':
            page.get_by_role('button', name='一键复制给本地 Agent', exact=True).click()
            instructions = page.evaluate('window.privateAgentInstructions')
            assert 'dual-fake-secret' not in instructions and '/path/task.json' not in instructions
            assert 'task_token' not in instructions and 'SHA-256' in instructions
            claim = json.loads(instructions.strip().splitlines()[-1])
            storage = page.evaluate('JSON.stringify({local: {...localStorage}, session: {...sessionStorage}})')
            assert claim['code'] not in storage and 'dual-fake-secret' not in storage
            page.screenshot(path=str(output / 'local-agent-handoff.png'), full_page=True)
            # Real production CLI path with fixture-only TLS roots and target policy injection.
            result = context.request.post(args.base + '/fixture/agent', data=claim).json()
            assert result['exit_code'] == 0, result['output']
            page.evaluate('delete window.privateAgentInstructions')
            instructions, claim = None, None
        page.get_by_role('button', name='查看基础报告', exact=True).click(timeout=15000)
        chart = page.get_by_role('region', name='并发与 P95 延迟', exact=True)
        expect(chart.locator('circle')).to_have_count(1)
        run_id = page.evaluate("localStorage.getItem('inferlab:last-run')")
        report = context.request.get(args.base + f'/api/v1/runs/{run_id}/report').json()
        assert report['execution']['mode'] == mode and report['success_count'] == 2
        for width in (1440, 390, 320):
            page.set_viewport_size({'width': width, 'height': 1100})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), f'report overflow {width}'
            page.screenshot(path=str(output / f'{mode}-report-{width}.png'), full_page=True)
        page.reload(wait_until='networkidle')
        page.get_by_role('button', name='报告', exact=True).click()
        expect(chart.locator('circle')).to_have_count(1)
        results.append({'mode': mode, 'completed': report['completed_count'], 'refresh_report_chart': 'passed'})
    assert not errors, errors
    (output / 'browser-result.json').write_text(json.dumps({'result': 'passed', 'source': 'Go TLS controlled upstream; no real provider', 'widths': [1440, 390, 320], 'runs': results, 'page_errors': errors}, indent=2) + '\n')
    browser.close()
print('Dual execution browser acceptance passed; private claim/code/key omitted from evidence.')
