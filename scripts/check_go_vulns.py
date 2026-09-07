#!/usr/bin/env python3
"""Run govulncheck and apply only exact, dated applicability exceptions."""
import datetime, json, pathlib, subprocess, sys
root = pathlib.Path(__file__).resolve().parents[1]
exceptions = json.loads((root/'scripts/go-vuln-exceptions.json').read_text())
now = datetime.datetime.now(datetime.timezone.utc).date()
expired = [x for x in exceptions if datetime.date.fromisoformat(x['expires']) <= now]
if expired:
 print('Expired vulnerability reviews: ' + ', '.join(x['id'] for x in expired)); sys.exit(1)
proc = subprocess.run(['govulncheck', '-json', './...'], cwd=root, capture_output=True, text=True)
if proc.returncode not in (0, 3):
 print(proc.stderr, file=sys.stderr); print(proc.stdout, file=sys.stderr); sys.exit(proc.returncode or 1)
decoder = json.JSONDecoder(); data = proc.stdout; position = 0; findings = []
while position < len(data):
 while position < len(data) and data[position].isspace(): position += 1
 if position == len(data): break
 item, position = decoder.raw_decode(data, position)
 if 'finding' in item: findings.append(item['finding'])
# JSON also includes module/package-level notices that are not reachable.
# Match the scanner's source-mode policy: fail callable vulnerable symbols;
# retain other notices in the job log for dependency hygiene.
reachable_ids = {f['osv'] for f in findings if f.get('trace', [{}])[0].get('function')}
not_reached = sorted({f['osv'] for f in findings} - reachable_ids)
for osv in not_reached: print('Informational: '+osv+' has no reachable vulnerable symbol in this build.')
findings = [f for f in findings if f['osv'] in reachable_ids]
failed = []; seen = set()
for finding in findings:
 trace = finding.get('trace', [{}])[0]
 key = (finding['osv'], trace.get('module'), trace.get('version'))
 if key in seen: continue
 seen.add(key)
 allowed = next((x for x in exceptions if key == (x['id'],x['module'],x['version'])), None)
 if allowed: print(f"Reviewed exception {key[0]} until {allowed['expires']}: {allowed['reason']}")
 else: failed.append(key); print('REVIEW REQUIRED: '+str(key))
if not findings: print('No Go vulnerability findings.')
if proc.returncode and not findings: print('Scanner failed without findings.',file=sys.stderr); sys.exit(1)
sys.exit(bool(failed))
