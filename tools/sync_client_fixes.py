"""Generate the APK's maintained UI fixes from client2/fixes; no server copy."""
from pathlib import Path
import argparse
import json

ROOT = Path(__file__).resolve().parents[2] / 'client2'
SOURCES = ['ui_waits.lua', 'battle_pipeline.lua', 'lineup_positions.lua',
           'gameplay_ui.lua', 'rank_bands.lua', 'refine_ui.lua', 'lingxue_ui.lua', 'team_state.lua', 'shop_materials.lua', 'recruit_ui.lua', 'friend_list.lua', 'tree_state.lua', 'shop_scroll.lua', 'card_frames.lua', 'ascension.lua',
           'weekly_draft.lua', 'sword_replay.lua',
           'recruit_scroll.lua', 'equipment_data.lua', 'daily_rewards.lua',
           'star_colors.lua', 'formation_touch.lua', 'assistance_ui.lua', 'assistance_cards.lua',
           'alchemy_confirmation.lua', 'tianjiang_badges.lua', 'gem_bag_batch.lua', 'gem_bag_data.lua', 'pet_data.lua', 'daily_first_tab.lua', 'silver_batch.lua', 'party_member_sort.lua', 'magic_tips.lua', 'party_honor.lua', 'party_territory.lua', 'newbie_task_data.lua',
           'energy_items.lua', 'startup_loading.lua',
           'frame_destroy_confirm.lua', 'huopao_desc.lua', 'territory_debug.lua']


def generate():
    source = ROOT / 'fixes'
    coverage = json.loads((source / 'legacy_coverage.json').read_text(encoding='utf-8'))
    assert set(coverage) == {f'{i:02}' for i in range(1, 23)}, 'legacy hotfix review is incomplete'
    for number, review in coverage.items():
        if 'source' in review:
            name = review['source']
            assert name in SOURCES and (source / name).is_file(), f'missing migrated hotfix {number}'
        elif 'baseline' in review:
            assert review.get('reason'), f'missing baseline review {number}'
            assert all((ROOT / name).is_file() for name in review['baseline']), f'missing native replacement {number}'
        else:
            assert review.get('retired'), f'unclassified legacy hotfix {number}'
    parts = [(source / 'bootstrap.lua').read_text(encoding='utf-8')]
    for name in SOURCES:
        text = (source / name).read_text(encoding='utf-8')
        if name == 'ui_waits.lua':
            text = '__onreq("signal",function(signal)\n' + text + '\nend)\n'
        parts.append('-- source: fixes/' + name + '\ndo\nlocal function install()\n'
                     + text + '\nend\ninstall()\nend\n')
    parts.append((source / 'xiuxing_cache.lua').read_text(encoding='utf-8'))
    return '\n'.join(parts)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    expected = generate()
    target = ROOT / 'apk-src/assets/lib/local_fixes.lua'
    if args.apply:
        target.unlink()  # Break hard links to previous APK staging trees.
        target.write_text(expected, encoding='utf-8', newline='\n')
    assert target.read_text(encoding='utf-8') == expected, 'run sync_client_fixes.py --apply'
    print('PASS: APK UI fixes match the maintained client2/fixes sources')
