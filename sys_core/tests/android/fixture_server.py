"""Isolated HTTP fixture for the standalone Android JNI upload test. No credentials."""
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from urllib.parse import urlparse,parse_qs
import argparse,json,threading,uuid
parser=argparse.ArgumentParser();parser.add_argument('--report',required=True);args=parser.parse_args()
sessions={};completed=[];lock=threading.Lock();expired=False
class Handler(BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def json(self,data,code=200):
        body=json.dumps({'code':code,'message':'fixture','data':data}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
    def do_POST(self):
        global expired
        path=urlparse(self.path);body=self.rfile.read(int(self.headers.get('Content-Length','0')));query=parse_qs(path.query)
        with lock:
            if path.path=='/v1/net/speedtest/upload':return self.json({'node':'fixture-backend'})
            if path.path=='/v1/file/upload/init':
                desc=json.loads(body);sid=str(uuid.uuid4());sessions[sid]={'desc':desc,'received':{}};return self.json({'upload_id':sid,'chunk_size':desc['chunk_size'],'chunk_count':desc['chunk_count'],'missing':list(range(desc['chunk_count']))})
            if path.path=='/v1/file/upload/chunk':
                session=sessions[query['upload_id'][0]];index=int(query['index'][0]);desc=session['desc']
                if desc['name']=='test-fixed.bin' and not expired:expired=True;return self.json(None,404)
                offset=index*desc['chunk_size'];expected=bytes((i*17+3)%251 for i in range(offset,min(offset+desc['chunk_size'],desc['total_size'])))
                if body!=expected:return self.json({'error':'payload mismatch'},422)
                session['received'][index]=body;return self.json({'index':index,'complete':len(session['received'])==desc['chunk_count']})
            if path.path=='/v1/file/upload/abandon':return self.json({})
            if path.path=='/v1/file/upload/complete':
                sid=json.loads(body)['upload_id'];session=sessions[sid];desc=session['desc']
                if len(session['received'])!=desc['chunk_count']:return self.json(None,400)
                record={'name':desc['name'],'bytes':desc['total_size'],'chunks':desc['chunk_count'],'received':sum(map(len,session['received'].values())),'payload_verified':True};completed.append(record)
                with open(args.report,'w',encoding='utf-8') as f:json.dump({'completed':completed,'session_404_injected':expired},f,indent=2)
                if desc.get('quick_share'):return self.json({'share_code':sid,'temp_name':sid,'file_name':desc['name'],'file_size':desc['total_size'],'expire_time':'fixture','url_path':'/share/'+sid})
                return self.json({'file_name':desc['name'],'storage_path':'/fixture/'+desc['name'],'file_size':desc['total_size'],'file_hash':desc['file_hash']})
            return self.json(None,404)
    def do_GET(self):
        if self.path.startswith('/share/'):
            sid=self.path.rsplit('/',1)[-1]
            if sid not in sessions:self.send_error(404);return
            total=sessions[sid]['desc']['total_size'];self.send_response(206);self.send_header('Content-Disposition','attachment; filename="fixture.bin"');self.send_header('Content-Range',f'bytes 0-0/{total}');self.send_header('Content-Length','1');self.end_headers();self.wfile.write(bytes([3]));return
        if self.path=='/__shutdown':self.send_response(200);self.end_headers();threading.Thread(target=self.server.shutdown,daemon=True).start();return
        self.send_error(404)
server=ThreadingHTTPServer(('127.0.0.1',0),Handler);print(json.dumps({'port':server.server_port}),flush=True);server.serve_forever();server.server_close()
