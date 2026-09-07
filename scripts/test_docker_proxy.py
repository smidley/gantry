#!/usr/bin/env python3
"""Exercise the real proxy against a disposable mock; never mounts the host daemon."""
import pathlib, subprocess, tempfile, uuid

root = pathlib.Path(__file__).resolve().parents[1]
tag = 'gantry-proxy-test-' + uuid.uuid4().hex[:10]
mock = '''import http.server, socketserver, os
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200); self.send_header('Content-Length','2'); self.end_headers(); self.wfile.write(b'{}')
 def do_HEAD(self): self.send_response(200); self.end_headers()
 def log_message(self,*args): pass
class Server(socketserver.UnixStreamServer): allow_reuse_address=True
socket_path='/run/proxy/mock.sock'
server=Server(socket_path, Handler)
os.chmod(socket_path,0o666)
server.serve_forever()
'''
client = '''import http.client, socket, time
class Client(http.client.HTTPConnection):
 def connect(self):
  self.sock=socket.socket(socket.AF_UNIX); self.sock.connect('/run/proxy/docker.sock')
for attempt in range(100):
 try:
  c=Client('localhost',timeout=3); c.request('GET','/_ping'); r=c.getresponse(); r.read()
  if r.status==200: break
 except (OSError, http.client.HTTPException): pass
 time.sleep(.1)
else: raise RuntimeError('mock proxy did not become ready')
allowed=['/_ping','/version','/info','/events','/containers/json?all=1','/images/json','/system/df','/v1.51/containers/'+'a'*64+'/stats?stream=false','/v1.51/containers/'+'a'*64+'/json','/containers/'+'a'*12+'/logs?follow=1']
for path in allowed:
 for method in ['GET','HEAD']:
  c=Client('localhost',timeout=3); c.request(method,path); r=c.getresponse(); r.read(); assert r.status==200,(method,path,r.status); c.close()
for method,path in [('POST','/containers/create'),('DELETE','/images/'+'a'*64),('POST','/_ping'),('PUT','/info'),('GET','/containers/'+'a'*64+'/archive?path=/etc/passwd'),('GET','/containers/%2e%2e/json'),('GET','//containers/json'),('GET','/containers/json/../create'),('GET','/v1.51/containers/json%2f..%2fcreate')]:
 c=Client('localhost',timeout=3); c.request(method,path); r=c.getresponse(); r.read(); assert r.status==403,(method,path,r.status); c.close()
print('Proxy allowlist: 20 allowed and 9 denied requests passed against mock Docker.')
'''
def docker(*args, **kwargs): return subprocess.run(['docker', *args], check=True, **kwargs)
with tempfile.TemporaryDirectory(prefix=tag) as folder:
 folder=pathlib.Path(folder); folder.chmod(0o755)
 (folder/'mock.py').write_text(mock); (folder/'client.py').write_text(client)
 config=(root/'deploy/monitoring/haproxy.cfg').read_text().replace('/var/run/docker.sock','/run/proxy/mock.sock')
 (folder/'haproxy.cfg').write_text(config)
 try:
  docker('volume','create',tag,stdout=subprocess.DEVNULL)
  docker('run','-d','--name',tag+'-mock','--network','none','-v',tag+':/run/proxy','-v',str(folder)+':/test:ro','python:3.13-alpine','python','/test/mock.py',stdout=subprocess.DEVNULL)
  docker('run','-d','--name',tag+'-proxy','--network','none','--user','0:65532','--cap-drop','ALL','--read-only','-v',tag+':/run/proxy','-v',str(folder/'haproxy.cfg')+':/usr/local/etc/haproxy/haproxy.cfg:ro','haproxy:3.2.23-alpine',stdout=subprocess.DEVNULL)
  docker('exec','--user','65532:65532',tag+'-mock','python','/test/client.py')
 except subprocess.CalledProcessError:
  subprocess.run(['docker','logs',tag+'-proxy'])
  subprocess.run(['docker','logs',tag+'-mock'])
  raise
 finally:
  subprocess.run(['docker','rm','-f',tag+'-proxy',tag+'-mock'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  subprocess.run(['docker','volume','rm',tag],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
