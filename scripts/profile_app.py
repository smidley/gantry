#!/usr/bin/env python3
"""Read-only HTTP workload receipt for representative fleets and aged databases."""
import argparse, concurrent.futures, json, pathlib, statistics, threading, time, urllib.error, urllib.parse, urllib.request

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--base-url',default='http://127.0.0.1:8380')
p.add_argument('--seconds',type=int,default=60)
p.add_argument('--viewers',type=int,default=4)
p.add_argument('--history-hours',type=int,default=24)
p.add_argument('--cookie-file',type=pathlib.Path,help='File containing the session Cookie header value; never included in output')
p.add_argument('--out',type=pathlib.Path,required=True)
a=p.parse_args()
if not (1<=a.viewers<=16 and 1<=a.seconds<=86400 and 1<=a.history_hours<=24*365): p.error('viewers 1–16, seconds 1–86400, history-hours 1–8760')
headers={'Accept':'application/json'}
if a.cookie_file: headers['Cookie']=a.cookie_file.read_text().strip()
base=a.base_url.rstrip('/')
def fetch(path):
 with urllib.request.urlopen(urllib.request.Request(base+path,headers=headers),timeout=20) as r:
  body=r.read(); return r.status,body
status,body=fetch('/api/live/snapshot'); snapshot=json.loads(body)
names=sorted(snapshot.get('containers',{})); names=names[:16]
records=[]; footprints=[]; lock=threading.Lock(); start=time.monotonic(); deadline=start+a.seconds

def viewer(index):
 lap=0
 while time.monotonic()<deadline:
  now=int(time.time()); name=names[(lap+index)%len(names)] if names else ''
  requests=[('snapshot','/api/live/snapshot'),('history','/api/series?'+urllib.parse.urlencode({'kind':'container','entity':name,'metrics':'cpu.pct,mem.bytes','from':now-a.history_hours*3600,'to':now})),('top','/api/top?resource=cpu&window=1h&agg=avg&limit=16')]
  kind,path=requests[lap%len(requests)]; began=time.monotonic(); size=points=0; code=0
  try:
   code,body=fetch(path);size=len(body);parsed=json.loads(body)
   if kind=='history': points=sum(len(s.get('points',[])) for s in parsed)
   if kind=='snapshot':
    host=parsed.get('host',{})
    with lock: footprints.append({'cpu_pct':host.get('gantry.cpu_pct'),'rss_bytes':host.get('gantry.rss_bytes')})
  except urllib.error.HTTPError as e: code=e.code
  except (OSError,ValueError): code=0
  with lock: records.append({'endpoint':kind,'status':code,'ms':(time.monotonic()-began)*1000,'bytes':size,'points':points})
  lap+=1
  time.sleep(max(0,min(deadline-time.monotonic(),1-(time.monotonic()-began))))
with concurrent.futures.ThreadPoolExecutor(max_workers=a.viewers) as pool: list(pool.map(viewer,range(a.viewers)))
summary={}
for kind in ['snapshot','history','top']:
 rows=[r for r in records if r['endpoint']==kind]; times=sorted(r['ms'] for r in rows)
 if rows: summary[kind]={'requests':len(rows),'median_ms':statistics.median(times),'p95_ms':times[min(len(times)-1,int(len(times)*.95))],'errors':sum(r['status']!=200 for r in rows),'mean_bytes':statistics.mean(r['bytes'] for r in rows),'history_points':sum(r['points'] for r in rows)}
try: _,settings=fetch('/api/settings'); database_bytes=json.loads(settings).get('database_bytes')
except (OSError,ValueError): database_bytes=None
receipt={'started_unix':int(time.time()-(time.monotonic()-start)),'duration_seconds':time.monotonic()-start,'viewers':a.viewers,'fleet_size':len(snapshot.get('containers',{})),'history_hours':a.history_hours,'database_bytes':database_bytes,'endpoints':summary,'footprint':footprints,'limitations':'HTTP sampling workload; no claim about browser frames, GPU correctness, long-term database growth, or unobserved history.'}
a.out.parent.mkdir(parents=True,exist_ok=True);a.out.write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({k:v for k,v in receipt.items() if k!='footprint'},indent=2))
