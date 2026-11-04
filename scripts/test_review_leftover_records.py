import json
from pathlib import Path
import tempfile
import unittest

from review_leftover_records import inspect_source


class RetainedEvidenceTests(unittest.TestCase):
    def inspect(self, rows):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'source.jsonl'
            path.write_text(''.join(json.dumps(row)+'\n' for row in rows))
            return inspect_source(path, {742913})

    def test_native_custom_and_legacy_tool_results_both_remain_candidates(self):
        rows = [dict(type='response_item', ordinal=n, payload=dict(type=kind, call_id=str(n), output='PID 742913 private-user-context'))
                for n, kind in enumerate(['function_call_output', 'custom_tool_call_output'])]
        result = self.inspect(rows)
        self.assertEqual(result['pid_output_mentions'], 2)
        self.assertEqual(len(result['matched_pid_output_events']), 2)
        self.assertIn('unconfirmed', result['ownership'])
        self.assertNotIn('private-user-context', json.dumps(result))
        self.assertTrue(result['stable'])

    def test_user_request_detected_but_injected_agent_instructions_ignored(self):
        def message(text):
            return dict(type='response_item', payload=dict(type='message', role='user',
                        content=[dict(type='input_text', text=text)]))
        instructions=message('# AGENTS.md instructions <INSTRUCTIONS>Keep the server running</INSTRUCTIONS>')
        self.assertFalse(self.inspect([instructions])['retention_request_candidate'])
        self.assertTrue(self.inspect([instructions, message('Please keep the server running')])['retention_request_candidate'])


if __name__ == '__main__':
    unittest.main()
