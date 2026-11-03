#!/usr/bin/env python3
"""Exact partial-reader projections retain valid native fields beside a named fault."""
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import partial_reads as partial


def failed(checks):
    return [row['field'] for row in checks if row['expected'] != row['actual']]


class PartialReadTests(unittest.TestCase):
    def test_callback_ticks_stop_after_failure_only_for_the_bound_actor(self):
        with tempfile.TemporaryDirectory() as root:
            folder = Path(root)
            (folder / 'beat').mkdir()
            (folder / 'home').mkdir()
            scenario, chat_path, owner, resources = [folder / name for name in ('scenario.json', 'chat.json', 'owner.json', 'resources.json')]
            chat = {'key': 'actor', 'name': 'actor', 'session_id': 'owned', 'terminal': {'backend': 'herdr', 'pane': 'p1'},
                    'scenario': str(scenario)}
            carry = folder / 'carry.json'
            carry.write_text(json.dumps({'chats': [chat]}))
            chat_path.write_text(json.dumps(chat))
            owner.write_text(json.dumps({'backend': 'herdr', 'pane': 'p2'}))
            env = {'JOURNEY_ROOT': root, 'HOME': str(folder / 'home'), 'JOURNEY_CARRY': str(carry)}
            operation, slug = 'account', 'statusline-warns-on-partial-account-read'
            with patch.dict(os.environ, env):
                stop, wrong = folder / ('stop-' + operation), folder / ('wrong-' + operation)
                scenario.write_text(json.dumps({'idle_statusline_refresh': {'stop_when_exists': str(stop)}}))
                resources.write_text(json.dumps({'idle_stop': str(wrong)}))
                prefix = folder / 'beat' / operation
                with patch.object(partial.PartialRead, operation, side_effect=ValueError('fixture failure')) as execute:
                    with self.assertRaisesRegex(ValueError, '^PARTIAL-IDLE-STOP-MISMATCH:'):
                        partial.run(slug, str(chat_path), str(owner), str(resources), str(prefix))
                    execute.assert_not_called()
                    self.assertEqual('absent', 'present' if wrong.exists() else 'absent', str(wrong))
                    resources.write_text(json.dumps({'idle_stop': str(stop)}))
                    with self.assertRaisesRegex(ValueError, '^fixture failure$'):
                        partial.run(slug, str(chat_path), str(owner), str(resources), str(prefix))
                    execute.assert_called_once()
                    self.assertEqual('complete\n', stop.read_text() if stop.is_file() else 'n/a:absent', str(stop))

    def test_native_input_receipt_matches_the_public_sorted_inline_input(self):
        steps, expected = partial.turn_input('native-order')
        wanted = 'native-order mock-engine: [{"busy_ms":20,"reply":"native-order-answer","tokens":{"cache_creation":0,"cache_read":0,"context_window":200000,"input":1000,"output":100},"type":"turn"}]'
        self.assertEqual(wanted, expected)
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'steps.json'
            partial.write_json(path, steps)
            with patch('chat.text') as text, patch('chat.keys'):
                partial.inline({'backend': 'herdr', 'pane': 'synthetic'}, path, 'native-order')
            self.assertEqual(wanted, text.call_args.args[1])

    def test_headless_inputs_stage_native_fault_and_actual_environment_witness_once(self):
        with tempfile.TemporaryDirectory() as root:
            folder = Path(root)
            (folder / 'beat').mkdir()
            (folder / 'home').mkdir()
            base = folder / 'scenario.json'
            base.write_text(json.dumps({'version': 'fixture', 'source_checkouts': {'synthetic/catalog': '/synthetic/source'}}))
            prefix = folder / 'beat' / 'reader'
            env = {'JOURNEY_ROOT': root, 'HOME': str(folder / 'home'), 'MOCK_ENGINE_SCENARIO': str(base),
                   'PFM_BEAT_FILE': str(folder / 'beat.json')}
            with patch.dict(os.environ, env), patch('uuid.uuid4', return_value='11111111-1111-4111-8111-111111111111'):
                path, records, witness = partial.headless_inputs(prefix)
                value = json.loads(path.read_text())
                self.assertEqual('11111111-1111-4111-8111-111111111111', value['session_id'])
                self.assertEqual(True, value['headless_usage_error'])
                self.assertEqual('claude-sonnet-4-6', value['model'])
                self.assertEqual({'synthetic/catalog': '/synthetic/source'}, value['source_checkouts'])
                self.assertEqual(str(records), value['record_dir'])
                self.assertEqual("printf '%s\\n' \"$CLAUDE_CONFIG_DIR\" > " + str(witness), value['steps'][0]['input']['command'])
                self.assertEqual({'type': 'turn', 'reply': 'partial-reader-headless-answer', 'busy_ms': 20,
                                  'tokens': {'input': 1000, 'output': 100, 'cache_read': 0, 'cache_creation': 0, 'context_window': 200000}}, value['steps'][1])
                self.assertFalse(records.exists())
                self.assertFalse(witness.exists())
                with self.assertRaisesRegex(ValueError, '^PARTIAL-HEADLESS-INPUT-EXISTS:'):
                    partial.headless_inputs(prefix)

    def test_actor_selection_uses_actual_carried_identity_and_metadata(self):
        chat = {'key': 'reader', 'session_id': 'owned', 'terminal': {'backend': 'herdr', 'pane': 'p1'}, 'account': 2, 'engine': 'claude'}
        bad = {**chat, 'key': 'bad', 'session_id': 'bad', 'terminal': {'backend': 'herdr', 'pane': 'p2'}}
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'carry.json'
            path.write_text(json.dumps({'chats': [chat, bad]}))
            with patch.dict(os.environ, {'JOURNEY_CARRY': str(path)}):
                supplied = {**chat, 'account': 99}
                self.assertEqual(chat, partial.carried(supplied))
                for changed in ({'session_id': 'foreign'}, {'terminal': {'backend': 'herdr', 'pane': 'p2'}}):
                    with self.subTest(changed=changed), self.assertRaisesRegex(ValueError, '^PARTIAL-READ-CARRY:'):
                        partial.carried({**chat, **changed})
                reader = object.__new__(partial.PartialRead)
                reader.chat, reader.resources = chat, {'bad_chat': bad}
                self.assertEqual(bad, reader.bad_chat())
                reader.resources['bad_chat'] = chat
                with self.assertRaisesRegex(ValueError, '^PARTIAL-READ-BAD-ACTOR:'):
                    reader.bad_chat()

    def test_index_keeps_each_native_engine_and_names_only_its_bad_path(self):
        chats = [{'name': engine, 'engine': engine, 'session_id': engine} for engine in ('claude', 'codex', 'opencode')]
        observed = {'chats': [
            {'name': 'claude', 'stored': {'transcript': {'entries': [{'role': 'assistant', 'text': 'partial-index-claude-answer'}]}}},
            {'name': 'codex', 'stored': {'transcript': {'entries': [{'role': 'assistant', 'text': 'partial-index-codex-answer'}]}}},
            {'name': 'opencode', 'stored': {'transcript': {'attribution': [
                {'id': 'opencode', 'first_prompt': 'partial-index-opencode mock-engine: [{"busy_ms":20,"reply":"partial-index-opencode-answer","tokens":{"cache_creation":0,"cache_read":0,"context_window":200000,"input":1000,"output":100},"type":"turn"}]'},
                {'id': 'foreign', 'first_prompt': 'not the requested native session'}]}}}],
            'activity': {'records': [{'msg': 'index.unparsed_lines', 'path': '/synthetic/bad.jsonl', 'count': 1},
                                     {'msg': 'index.unparsed_lines', 'path': '/synthetic/foreign.jsonl', 'count': 9}]}}
        self.assertEqual([], failed(partial.index_checks(observed, chats, '/synthetic/bad.jsonl')))
        wrong = copy.deepcopy(observed)
        wrong['chats'][2]['stored']['transcript']['attribution'][0]['id'] = 'other'
        wrong['activity']['records'][0]['count'] = 2
        self.assertEqual(['indexed-opencode-prompt', 'named-unparsed-transcript'],
                         failed(partial.index_checks(wrong, chats, '/synthetic/bad.jsonl')))

    def test_tokens_keep_literal_price_and_named_read_error(self):
        chat = {'session_id': 'owned'}
        row = {'sid': 'owned', 'engine': 'claude', 'model': 'claude-sonnet-4-6', 'calls': 1,
               'tok': {'in': 1000, 'out': 100, 'cw5': 0, 'cw1': 0, 'cr': 0}, 'usd': 0.0045}
        document = {'runs': [row, {'sid': 'foreign'}],
                    'scan': {'readErrors': ['/synthetic/bad.jsonl: read /synthetic/bad.jsonl: is a directory']}}
        self.assertEqual([], failed(partial.token_checks(document, chat, '/synthetic/bad.jsonl')))
        for field, value, wanted in (('usd', 0, ['retained-priced-usage']),
                                      ('sid', 'foreign', ['retained-priced-usage'])):
            with self.subTest(field=field):
                wrong = copy.deepcopy(document)
                wrong['runs'][0][field] = value
                self.assertEqual(wanted, failed(partial.token_checks(wrong, chat, '/synthetic/bad.jsonl')))
        document['scan']['readErrors'] = []
        self.assertEqual(['named-unreadable-transcript'],
                         failed(partial.token_checks(document, chat, '/synthetic/bad.jsonl')))

    def test_name_convergence_checks_tab_store_and_picker(self):
        chat = {'name': 'partial-reader-good', 'session_id': 'owned', 'account': 2}
        row = {'name': 'partial-reader-good', 'picker': [{'name': 'partial-reader-good'}],
               'terminal': {'tab': {'tab': {'label': '🥈 PARTIAL-READER-GOOD'}}},
               'stored': {'chat': [{'uuid': 'owned', 'name': 'partial-reader-good'}, {'uuid': 'foreign', 'name': 'other'}]}}
        self.assertEqual([], failed(partial.named_checks({'chats': [row]}, chat)))
        row['terminal']['tab']['tab']['label'] = '🥇 PARTIAL-READER-GOOD'
        row['stored']['chat'][0]['name'] = 'stale'
        self.assertEqual(['preserved-good-tab', 'preserved-good-store'], failed(partial.named_checks({'chats': [row]}, chat)))
        chat['engine'] = 'claude'
        prior = {'terminal': {'identity': {'pane': 'p1'}}, 'process_births': [
            {'pid': 40, 'start': 'owned birth', 'chain': [{'argv': 'claude'}], 'environment': {}}]}
        after = copy.deepcopy(prior)
        self.assertEqual([], failed(partial.rename_birth_checks(prior, after, chat)))
        after['process_births'][0]['start'] = 'reused pid'
        self.assertEqual(['rename-keeps-native-birth'], failed(partial.rename_birth_checks(prior, after, chat)))
        prior['process_births'] = []
        with self.assertRaisesRegex(ValueError, '^PARTIAL-NAME-ENGINE-BIRTH:'):
            partial.rename_birth_checks(prior, after, chat)

    def test_headless_keeps_answer_diagnostic_account_and_actual_child_reaping(self):
        envelope = {'engine': 'cc', 'model': 'claude-sonnet-4-6', 'result': 'partial-reader-headless-answer',
                    'usage': {'input_tokens': 1000, 'cached_input_tokens': 0, 'cache_creation_input_tokens': 0, 'output_tokens': 100},
                    'total_cost_usd': 0.0012, 'diagnostics': [partial.HEADLESS_DIAGNOSTIC]}
        def output(value):
            return 'pfm headless: engine diagnostic: ' + partial.HEADLESS_DIAGNOSTIC + '\n' + json.dumps(value, separators=(',', ':')) + '\n'
        births = [{'pid': 40, 'start': 'owned birth'}]
        self.assertEqual([], failed(partial.headless_checks(output(envelope), {}, births, '/synthetic/account2', '/synthetic/account2')))
        self.assertEqual([], failed(partial.headless_checks(output(envelope), {40: {'start': 'another birth'}}, births,
                                                           '/synthetic/account2', '/synthetic/account2')))
        self.assertEqual(['headless-children-reaped', 'headless-account'], failed(partial.headless_checks(
            output(envelope), {40: {'start': 'owned birth'}}, births, '/synthetic/account2', '/synthetic/account1')))
        envelope['result'] = ''
        envelope['diagnostics'] = []
        self.assertEqual(['retained-headless-answer', 'named-headless-partial-usage'], failed(partial.headless_checks(
            output(envelope), {}, births, '/synthetic/account2', '/synthetic/account2')))
        envelope['result'], envelope['diagnostics'] = 'partial-reader-headless-answer', [partial.HEADLESS_DIAGNOSTIC]
        for field, value, expected in (('usage', None, 'retained-headless-usage'),
                                        ('total_cost_usd', 0, 'retained-headless-cost'),
                                        ('model', 'wrong', 'headless-model'), ('engine', 'cx', 'headless-engine')):
            with self.subTest(field=field):
                wrong = {**envelope, field: value}
                self.assertEqual([expected], failed(partial.headless_checks(
                    output(wrong), {}, births, '/synthetic/account2', '/synthetic/account2')))
        for absent in ([], [{'pid': 40}], births * 2):
            with self.subTest(births=absent), self.assertRaisesRegex(ValueError, '^PARTIAL-HEADLESS-BIRTH:'):
                partial.headless_checks(output(envelope), {}, absent, '/synthetic/account2', '/synthetic/account2')

    def test_headless_unreadable_envelope_never_looks_like_empty_answer(self):
        with self.assertRaisesRegex(ValueError, '^PARTIAL-HEADLESS-ENVELOPE:'):
            partial.headless_checks('not a native envelope\n', {}, [{'pid': 40, 'start': 'owned birth'}], 'account', 'account')

    def test_warning_requires_exact_message_and_owned_subject(self):
        records = [{'msg': 'index.unparsed_lines', 'path': '/synthetic/owned', 'count': 1},
                   {'msg': 'index.unparsed_lines', 'path': '/synthetic/foreign', 'count': 2},
                   {'msg': 'index.unreadable', 'path': '/synthetic/owned', 'count': 3}]
        self.assertEqual([records[0]], partial.warning(records, 'index.unparsed_lines', 'path', '/synthetic/owned'))
        self.assertEqual([], partial.warning(records, 'index.unparsed_lines', 'path', '/synthetic/missing'))

    def test_statusline_partial_account_read_keeps_exact_subject_and_cause(self):
        cache = '/synthetic/acct-2.json'
        cause = 'statusline: usage cache /synthetic/acct-2.json: decode usage cache /synthetic/acct-2.json: unexpected end of JSON input (missing windows show ERR)'
        records = [{'msg': 'statusline.warn', 'err': cause},
                   {'msg': 'statusline.warn', 'err': 'statusline: usage cache /synthetic/foreign.json: other error'}]
        self.assertEqual([], failed(partial.account_warning_checks(records, cache)))
        records[0]['err'] = 'statusline: usage cache /synthetic/acct-2.json: unrelated error'
        self.assertEqual(['named-partial-account-cause'], failed(partial.account_warning_checks(records, cache)))
        for cell, expected in (('7d-fable-used:ERR', []), ('7d-fable-used:—', ['partial-account-error-cell'])):
            with self.subTest(cell=cell):
                self.assertEqual(expected, failed(partial.account_error_checks('5h-used:89% │ ' + cell + ' │ 7d-used:34%')))

    def test_name_sync_names_the_actual_socket_read_refusal(self):
        records = [{'msg': 'index.unreadable', 'path': '/synthetic/owned.jsonl', 'kind': 'claude transcript',
                    'err': 'open "/synthetic/owned.jsonl": open /synthetic/owned.jsonl: no such device or address'},
                   {'msg': 'index.unreadable', 'path': '/synthetic/foreign.jsonl', 'kind': 'claude transcript', 'err': 'foreign'}]
        self.assertEqual([], failed(partial.name_warning_checks(records, '/synthetic/owned.jsonl')))
        records[0]['err'] = 'unrelated error'
        self.assertEqual(['named-name-sync-partial-read'], failed(partial.name_warning_checks(records, '/synthetic/owned.jsonl')))


if __name__ == '__main__':
    unittest.main()
