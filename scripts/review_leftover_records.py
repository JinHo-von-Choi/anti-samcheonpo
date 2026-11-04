#!/usr/bin/env python3
"""Review retained leftover verdicts without labelling ownership or killing PIDs.

Input: read-only ledger export with verdict_id, session_id, facts and
session_started_at. Output deliberately excludes raw commands and paths.
"""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import re


def inspect_source(path, pids):
    result = dict(available=False, pid_metadata_matches=0, pid_output_mentions=0,
                  retention_request_candidate=False, skipped_lines=0, matched_pid_output_events=[])
    source = Path(path) if path else None
    if source is None or not source.is_file():
        return result
    before = source.stat()
    digest = hashlib.sha256()
    result["available"] = True
    with source.open('rb') as stream:
        for line in stream:
            digest.update(line)
            if len(line) > 8 << 20:
                result["skipped_lines"] += 1
                continue
            try:
                row = json.loads(line)
            except ValueError:
                result["skipped_lines"] += 1
                continue
            payload = row.get('payload') or {}
            if row.get('type') == 'session_meta':
                for key in ('pid', 'ppid', 'process_id', 'parent_pid', 'host_pid'):
                    if payload.get(key) in pids:
                        result['pid_metadata_matches'] += 1
            messages = []
            if row.get('type') == 'event_msg' and payload.get('type') == 'user_message':
                messages.append(str(payload.get('message', '')))
            if row.get('type') == 'response_item' and payload.get('type') == 'message' and payload.get('role') == 'user':
                messages.extend(str(p.get('text', '')) for p in payload.get('content', []) if isinstance(p, dict) and p.get('type') == 'input_text')
            for message in messages:
                if '# AGENTS.md instructions' in message or '<INSTRUCTIONS>' in message:
                    continue
                if re.search(r'(?:keep|leave|유지|켜두|살려).{0,40}(?:server|서버)|(?:server|서버).{0,40}(?:keep|leave|유지|켜두|살려)', message, re.I):
                    result['retention_request_candidate'] = True
            if payload.get('type') in ('function_call_output', 'custom_tool_call_output'):
                output = str(payload.get('output', ''))
                mentioned = {int(pid) for pid in re.findall(r'\b\d{2,}\b', output)} & pids
                result['pid_output_mentions'] += len(mentioned)
                if mentioned and len(result['matched_pid_output_events']) < 16:
                    result['matched_pid_output_events'].append(dict(ordinal=row.get('ordinal'), pids=sorted(mentioned),
                        call_id_hash=hashlib.sha256(str(payload.get('call_id','')).encode()).hexdigest()))
    after = source.stat()
    result['sha256'] = digest.hexdigest()
    result['stable'] = before.st_size == after.st_size and before.st_mtime_ns == after.st_mtime_ns
    result['ownership'] = 'unconfirmed; PID mention alone does not prove ancestry or user retention intent'
    return result


def review(rows, before):
    boot = next(int(line.split()[1]) for line in Path('/proc/stat').read_text().splitlines() if line.startswith('btime '))
    records, roles, identities = [], collections.Counter(), collections.Counter()
    for row in rows:
        if row['session_started_at'] >= before:
            continue
        facts = json.loads(row['facts'])
        births = {int(pid): int(start) for pid, start in re.findall(r'(\d+)@(\d+)', facts.get('target_hash', ''))}
        processes = []
        for pid_text, command in re.findall(r'(?:^|; )pid (\d+) (.*?)(?=; pid |$)', facts.get('procs', '')):
            pid = int(pid_text)
            role = ('codex_host_signature' if '@openai/codex' in command else
                    'monitor_hook_signature' if 'samcheonpo-hook hook Stop' in command else
                    'tool_server_candidate' if 'mcp' in command.lower() else
                    'sleep_test_candidate' if command.startswith('sleep ') else 'shell_or_unknown')
            roles[role] += 1
            try:
                stat = (Path('/proc') / str(pid) / 'stat').read_text().rsplit(')', 1)[1].split()
                current = ('same_second_birth_candidate' if births.get(pid) == int(boot+int(stat[19])/100)
                           else 'pid_reused_or_birth_unknown')
            except (FileNotFoundError, ProcessLookupError):
                current = 'not_present'
            except PermissionError:
                current = 'inaccessible'
            identities[current] += 1
            processes.append(dict(pid=pid, started_unix=births.get(pid), current_identity=current,
                                  retained_command_role=role, historical_ownership='unconfirmed',
                                  normal_server_status='unconfirmed',
                                  reason='historical PPID/ancestry and user retention intent not recorded'))
        records.append(dict(verdict_id=row['verdict_id'], session_id=row['session_id'],
                            session_started_at=row['session_started_at'], retained_process_count=facts.get('count'),
                            processes=processes, source_review=inspect_source(row.get("source_path"), set(births)), original_facts_sha256=hashlib.sha256(row['facts'].encode()).hexdigest(),
                            review_status='reviewed_retained_evidence', human_label=False, cleanup_action='none'))
    summary = dict(reviewed_records=len(records),
                   records_containing_codex_host_signature=sum(any(p['retained_command_role']=='codex_host_signature' for p in r['processes']) for r in records),
                   retained_process_roles=dict(roles), current_identity_status=dict(identities),
                   sources_inspected=sum(r["source_review"]["available"] for r in records),
                   sources_with_pid_metadata=sum(r["source_review"]["pid_metadata_matches"]>0 for r in records),
                   sources_with_pid_output_mentions=sum(r["source_review"]["pid_output_mentions"]>0 for r in records),
                   sources_with_retention_request_candidates=sum(r["source_review"]["retention_request_candidate"] for r in records),
                   historical_ownership_confirmed=0, normal_server_confirmed=0, processes_killed=0,
                   source_cohort_before_utc=before,
                   limitations=['retained command signature is not proof of session ownership or retention intent',
                                'current absence/reuse cannot reconstruct historical ancestry',
                                'derived evidence review, not an independent human label'])
    return records, summary


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--before', required=True)
    parser.add_argument('--expected-records', type=int, required=True)
    args = parser.parse_args()
    records, summary = review(json.loads(args.input.read_text()), args.before)
    if len(records) != args.expected_records:
        parser.error(f'cohort mismatch: {len(records)} records')
    args.out.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name, value in [('leftover-review.json', records), ('leftover-summary.json', summary)]:
        destination = args.out / name
        destination.write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n')
        destination.chmod(0o600)
    print(json.dumps(summary, ensure_ascii=False))
