"""Общий Telegram-бот. Секрет бота остаётся только на ПК владельца."""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import os
from pathlib import Path
import secrets
import sqlite3
import time

import aiohttp
from aiohttp import web

USERNAME = "songrequest_streamer_bot"
ROOT = Path(__file__).resolve().parent

def fingerprint(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()

class Relay:
    def __init__(self, path: Path, api):
        self.api = api
        self.db = sqlite3.connect(path)
        self.db.row_factory = sqlite3.Row
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.execute("PRAGMA synchronous=FULL")
        self.db.executescript('''
        CREATE TABLE IF NOT EXISTS devices(key_hash TEXT PRIMARY KEY, owner INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0, seen REAL NOT NULL DEFAULT 0);
        CREATE TABLE IF NOT EXISTS pairs(code_hash TEXT PRIMARY KEY, device TEXT UNIQUE NOT NULL, expires REAL NOT NULL, proposed INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL DEFAULT '', approved INTEGER NOT NULL DEFAULT 0);
        CREATE TABLE IF NOT EXISTS updates(id INTEGER PRIMARY KEY, owner INTEGER NOT NULL, payload TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS callbacks(id TEXT PRIMARY KEY, owner INTEGER NOT NULL, expires REAL NOT NULL);
        CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS notices(id INTEGER PRIMARY KEY AUTOINCREMENT,owner INTEGER NOT NULL,text TEXT NOT NULL);
        ''')
        self.db.commit()
        if 'due' not in {row[1] for row in self.db.execute('PRAGMA table_info(notices)')}:
            self.db.execute('ALTER TABLE notices ADD COLUMN due REAL NOT NULL DEFAULT 0')
            self.db.commit()
        self.me = {"id":0,"is_bot":True,"first_name":"songrequest","username":USERNAME}
        self.pair_limits = {}
        self.active_polls = set()
        self.send_limits = {}
        self.telegram_online = False

    def app(self):
        @web.middleware
        async def safe_errors(request,handler):
            try:return await handler(request)
            except web.HTTPException as error:
                if request.path.startswith('/api/telegram/'):
                    return web.json_response({'ok':False,'error_code':error.status},status=error.status)
                raise
            except asyncio.CancelledError:raise
            except Exception as error:
                logging.warning('Запрос отклонён: %s',type(error).__name__)
                return web.json_response({'ok':False,'error_code':503},status=503)
        app = web.Application(client_max_size=10*1024*1024,middlewares=[safe_errors])
        app.add_routes([web.get('/health',self.health),web.get('/stream',self.stream),web.post('/pair/start',self.pair_start),web.post('/pair/status',self.pair_status),web.post('/pair/confirm',self.pair_confirm),web.post('/pair/revoke',self.revoke),web.post('/api/telegram/{method}',self.telegram)])
        return app

    async def health(self, request):
        return web.json_response({"service":"songrequest-telegram-relay","bot":USERNAME,"telegram_online":self.telegram_online})

    def device(self, request, paired=False):
        header=request.headers.get('Authorization','')
        if not header.startswith('Bearer ') or len(header)>256:
            raise web.HTTPUnauthorized()
        row=self.db.execute('SELECT * FROM devices WHERE key_hash=? AND revoked=0',(fingerprint(header[7:]),)).fetchone()
        if row is None or (paired and not row['owner']):
            raise web.HTTPUnauthorized()
        return row

    async def pair_start(self, request):
        if request.headers.get('Origin'):
            raise web.HTTPForbidden()
        now=time.time()
        ip=request.headers.get('CF-Connecting-IP') or request.remote or ''
        self.pair_limits={key:value for key,value in self.pair_limits.items() if value[0]>now-60}
        count=self.pair_limits.get(ip,(now,0))[1]
        if count>=10 or len(self.pair_limits)>2000:
            raise web.HTTPTooManyRequests()
        self.pair_limits[ip]=(now,count+1)
        if self.db.execute('SELECT COUNT(*) FROM pairs WHERE expires>?',(now,)).fetchone()[0]>=500:
            raise web.HTTPTooManyRequests()
        key,code=secrets.token_urlsafe(32),secrets.token_urlsafe(24)
        with self.db:
            self.db.execute('DELETE FROM devices WHERE owner=0 AND key_hash IN (SELECT device FROM pairs WHERE expires<?)',(now,))
            self.db.execute('DELETE FROM pairs WHERE expires<?',(now,))
            self.db.execute('INSERT INTO devices(key_hash) VALUES(?)',(fingerprint(key),))
            self.db.execute('INSERT INTO pairs(code_hash,device,expires) VALUES(?,?,?)',(fingerprint(code),fingerprint(key),now+600))
        return web.json_response({"key":key,"link":f"https://t.me/{USERNAME}?start=link_{code}","expires_in":600})

    def binding(self, device):
        if device['owner']:
            return {"state":"connected","owner_id":device['owner'],"username":USERNAME}
        pair=self.db.execute('SELECT * FROM pairs WHERE device=? AND expires>?',(device['key_hash'],time.time())).fetchone()
        if not pair:
            return {"state":"expired"}
        return {"state":"confirm" if pair['approved'] else "waiting","owner_id":pair['proposed'],"name":pair['name'],"username":USERNAME}

    async def pair_status(self, request):
        return web.json_response(self.binding(self.device(request)))

    async def pair_confirm(self, request):
        device=self.device(request)
        if device['owner']:return web.json_response(self.binding(device))
        pair=self.db.execute('SELECT * FROM pairs WHERE device=? AND expires>? AND approved=1',(device['key_hash'],time.time())).fetchone()
        if not pair:
            raise web.HTTPConflict()
        owner=pair['proposed']
        with self.db:
            self.db.execute('UPDATE devices SET revoked=1 WHERE owner=? AND key_hash<>?',(owner,device['key_hash']))
            self.db.execute('UPDATE devices SET owner=?,seen=? WHERE key_hash=?',(owner,time.time(),device['key_hash']))
            self.db.execute('DELETE FROM pairs WHERE device=?',(device['key_hash'],))
            self.db.execute('INSERT INTO notices(owner,text) VALUES(?,?)',(owner,'✅ Приложение подключено. Отправь /start: отчёты и пульт доступны, пока приложение и компьютер автора включены.'))
        return web.json_response({"state":"connected","owner_id":owner,"username":USERNAME})

    async def revoke(self, request):
        device=self.device(request)
        with self.db:
            self.db.execute('UPDATE devices SET revoked=1 WHERE key_hash=?',(device['key_hash'],))
            self.db.execute('DELETE FROM pairs WHERE device=?',(device['key_hash'],))
        return web.json_response({"ok":True})

    async def target_allowed(self, owner, chat):
        if str(chat)==str(owner):
            return True
        if not str(chat).startswith(('@','-100')):
            return False
        result=await self.api('getChatMember',{'chat_id':chat,'user_id':owner})
        return result.get('ok') and result.get('result',{}).get('status') in ('creator','administrator')

    async def telegram(self, request):
        device=self.device(request,paired=True)
        owner=device['owner']
        method=request.match_info['method']
        if method not in ('getMe','getWebhookInfo','getUpdates','sendMessage','sendPhoto','sendDocument','answerCallbackQuery'):
            raise web.HTTPForbidden()
        if not self.telegram_online:return web.json_response({'ok':False,'error_code':503})
        with self.db:
            self.db.execute('UPDATE devices SET seen=? WHERE key_hash=?',(time.time(),device['key_hash']))
        files=[]
        if request.content_type.startswith('multipart/'):
            reader=await request.multipart()
            payload={}
            total=0
            while True:
                field=await reader.next()
                if field is None:break
                data=bytearray()
                while True:
                    chunk=await field.read_chunk()
                    if not chunk:break
                    total+=len(chunk)
                    if total>9*1024*1024:raise web.HTTPRequestEntityTooLarge(max_size=9*1024*1024,actual_size=total)
                    data.extend(chunk)
                if field.filename:
                    files.append((field.name,Path(field.filename).name,bytes(data)))
                else:payload[field.name]=data.decode('utf-8')
        else:
            payload=await request.json()
            if not isinstance(payload,dict):raise web.HTTPBadRequest()
        if method=='getMe':return web.json_response({'ok':True,'result':self.me})
        if method=='getWebhookInfo':return web.json_response({'ok':True,'result':{'url':''}})
        if method=='getUpdates':
            if device['key_hash'] in self.active_polls:
                return web.json_response({'ok':False,'error_code':409})
            self.active_polls.add(device['key_hash'])
            try:return await self.updates(request,device,payload)
            finally:self.active_polls.discard(device['key_hash'])
        now=time.time()
        recent=[stamp for stamp in self.send_limits.get(device['key_hash'],[]) if stamp>now-60]
        if len(recent)>=60:return web.json_response({'ok':False,'error_code':429,'parameters':{'retry_after':10}})
        recent.append(now)
        self.send_limits[device['key_hash']]=recent
        if method=='answerCallbackQuery':
            found=self.db.execute('SELECT 1 FROM callbacks WHERE id=? AND owner=? AND expires>?',(payload.get('callback_query_id',''),owner,time.time())).fetchone()
            if not found:raise web.HTTPForbidden()
        elif not await self.target_allowed(owner,payload.get('chat_id','')):
            raise web.HTTPForbidden()
        current=self.device(request,paired=True)
        if current['owner']!=owner:raise web.HTTPUnauthorized()
        allowed={'sendMessage':{'chat_id','text','reply_markup','disable_web_page_preview'},'sendPhoto':{'chat_id','caption'},'sendDocument':{'chat_id','caption'},'answerCallbackQuery':{'callback_query_id','text','show_alert'}}[method]
        payload={key:value for key,value in payload.items() if key in allowed}
        result=await self.api(method,payload,files)
        return web.json_response(result)

    async def updates(self,request,device,payload):
        owner=device['owner']
        with self.db:self.db.execute('UPDATE devices SET seen=? WHERE key_hash=?',(time.time(),device['key_hash']))
        try:offset=max(0,int(payload.get('offset',0)))
        except (ValueError,TypeError):raise web.HTTPBadRequest()
        with self.db:self.db.execute('DELETE FROM updates WHERE owner=? AND id<?',(owner,offset))
        deadline=time.monotonic()+20
        while True:
            if not self.telegram_online:return web.json_response({'ok':False,'error_code':503})
            current=self.db.execute('SELECT revoked,owner FROM devices WHERE key_hash=?',(device['key_hash'],)).fetchone()
            if not current or current['revoked'] or current['owner']!=owner:raise web.HTTPUnauthorized()
            rows=self.db.execute('SELECT payload FROM updates WHERE owner=? AND id>=? ORDER BY id LIMIT 50',(owner,offset)).fetchall()
            if rows or time.monotonic()>=deadline:
                return web.json_response({'ok':True,'result':[json.loads(row[0]) for row in rows]})
            if request.transport is None or request.transport.is_closing():raise asyncio.CancelledError()
            await asyncio.sleep(.25)

    async def stream(self,request):
        device=self.device(request,paired=True)
        if not self.telegram_online:raise web.HTTPServiceUnavailable()
        key=device['key_hash']
        if key in self.active_polls:raise web.HTTPConflict()
        socket=web.WebSocketResponse(heartbeat=30,max_msg_size=16384)
        await socket.prepare(request)
        self.active_polls.add(key)
        try:
            async for message in socket:
                if message.type!=aiohttp.WSMsgType.TEXT:break
                try:
                    payload=json.loads(message.data)
                    if not isinstance(payload,dict):raise web.HTTPBadRequest()
                    response=await self.updates(request,device,payload)
                    await socket.send_str(response.body.decode())
                except web.HTTPException as error:
                    await socket.send_json({'ok':False,'error_code':error.status})
                    break
        except asyncio.CancelledError:raise
        except Exception as error:
            logging.warning('Поток приложения закрыт: %s',type(error).__name__)
        finally:
            self.active_polls.discard(key)
            await socket.close()
        return socket

    def notice(self, owner,text):
        if self.db.execute('SELECT COUNT(*) FROM notices WHERE owner=?',(owner,)).fetchone()[0]>=20:return
        self.db.execute('INSERT INTO notices(owner,text) VALUES(?,?)',(owner,text))

    def route(self, update):
        """Только запись в транзакции, никаких сетевых вызовов до фиксации offset."""
        cb=update.get('callback_query')
        message=cb.get('message',{}) if cb else update.get('message',{})
        sender=cb.get('from',{}) if cb else message.get('from',{})
        owner=sender.get('id',0)
        if message.get('chat',{}).get('type')!='private' or message.get('chat',{}).get('id')!=owner or not owner:
            return
        text=message.get('text','') if not cb else ''
        if text.startswith('/start link_') or (cb and cb.get('data','').startswith('pair:')):
            code=text[len('/start link_'):].strip() if not cb else cb['data'][5:]
            pair=self.db.execute('SELECT * FROM pairs WHERE code_hash=? AND expires>?',(fingerprint(code),time.time())).fetchone()
            if not pair or (pair['proposed'] and pair['proposed']!=owner):
                self.notice(owner,'Ссылка подключения истекла или уже используется. Нажми «Подключить Telegram» в приложении заново.')
                return
            name=sender.get('username') or sender.get('first_name') or str(owner)
            self.db.execute('UPDATE pairs SET proposed=?,name=?,approved=1 WHERE code_hash=?',(owner,str(name)[:80],fingerprint(code)))
            self.notice(owner,'Теперь подтверди свой Telegram ID в окне приложения на компьютере. До подтверждения управление закрыто.')
            return
        if text=='/id':self.notice(owner,f'Твой Telegram ID: {owner}');return
        if text=='/support':self.notice(owner,'Поддержка: https://t.me/songrequest_wrapped_bot?start=support');return
        if text=='/unlink':
            self.db.execute('UPDATE devices SET revoked=1 WHERE owner=?',(owner,))
            self.db.execute('DELETE FROM updates WHERE owner=?',(owner,))
            self.notice(owner,'Приложение отключено. Для повторного подключения нажми кнопку Telegram в приложении.')
            return
        device=self.db.execute('SELECT * FROM devices WHERE owner=? AND revoked=0 ORDER BY seen DESC LIMIT 1',(owner,)).fetchone()
        if not device:
            self.notice(owner,'Открой songrequest на компьютере и нажми «Подключить Telegram». Токен бота не нужен. Поддержка: https://t.me/songrequest_wrapped_bot?start=support')
            return
        if device['seen']<time.time()-75:
            self.notice(owner,'Приложение сейчас не подключено. Запусти songrequest на компьютере и повтори команду. Старые команды управления не будут выполнены позже.')
            return
        self.db.execute('INSERT OR IGNORE INTO updates VALUES(?,?,?)',(update['update_id'],owner,json.dumps(update)))
        if cb:self.db.execute('INSERT OR REPLACE INTO callbacks VALUES(?,?,?)',(cb['id'],owner,time.time()+600))

    async def poll(self):
        while True:
            try:
                row=self.db.execute("SELECT value FROM settings WHERE key='offset'").fetchone()
                offset=int(row[0]) if row else 0
                result=await self.api('getUpdates',{'offset':offset,'timeout':25,'allowed_updates':['message','callback_query']})
                if not result.get('ok') and result.get('error_code')==401:raise PermissionError('Bot token revoked')
                if not result.get('ok'):raise RuntimeError('Telegram poll rejected')
                self.telegram_online=True
                with self.db:
                    for update in result['result']:
                        self.route(update)
                        offset=max(offset,update['update_id']+1)
                    self.db.execute("INSERT INTO settings VALUES('offset',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",(str(offset),))
                    self.db.execute('DELETE FROM callbacks WHERE expires<?',(time.time(),))
            except asyncio.CancelledError:raise
            except PermissionError:raise
            except Exception as error:
                self.telegram_online=False
                logging.warning('Связь общего бота повторится: %s',type(error).__name__)
                await asyncio.sleep(5)

    async def deliver_notices(self):
        while True:
            row=self.db.execute('SELECT * FROM notices WHERE due<=? ORDER BY id LIMIT 1',(time.time(),)).fetchone()
            if row:
                try:
                    result=await self.api('sendMessage',{'chat_id':row['owner'],'text':row['text']})
                    if not result.get('ok'):raise RuntimeError('notice rejected')
                    with self.db:self.db.execute('DELETE FROM notices WHERE id=?',(row['id'],))
                except asyncio.CancelledError:raise
                except Exception:
                    with self.db:self.db.execute('UPDATE notices SET due=? WHERE id=?',(time.time()+60,row['id']))
                    await asyncio.sleep(1)
            else:await asyncio.sleep(.5)

async def main():
    token=os.environ.get('SONGREQUEST_COMMON_BOT_TOKEN','')
    if not token:raise RuntimeError('Token not configured')
    runtime=ROOT/'runtime'
    runtime.mkdir(exist_ok=True)
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=40)) as session:
        async def api(method,payload,files=()):
            if files:
                data=aiohttp.FormData()
                for key,value in payload.items():data.add_field(key,str(value))
                for name,filename,content in files:data.add_field(name,content,filename=filename)
                kwargs={'data':data}
            else:kwargs={'json':payload}
            async with session.post('https://api.telegram.org/bot'+token+'/'+method,**kwargs) as response:
                result=await response.json()
                if not result.get('ok'):
                    return {'ok':False,'error_code':result.get('error_code',response.status),'parameters':result.get('parameters',{})}
                return result
        identity=await api('getMe',{})
        if not identity.get('ok') or identity['result'].get('username','').lower()!=USERNAME:
            raise RuntimeError('Wrong bot token')
        hook=await api('getWebhookInfo',{})
        if not hook.get('ok') or hook['result'].get('url'):raise RuntimeError('Existing webhook; not changed')
        relay=Relay(runtime/'relay.sqlite',api)
        relay.me=identity['result']
        runner=web.AppRunner(relay.app(),access_log=None)
        await runner.setup()
        await web.TCPSite(runner,'127.0.0.1',8980).start()
        logging.warning('Общий бот готов: 127.0.0.1:8980')
        tasks=[asyncio.create_task(relay.poll()),asyncio.create_task(relay.deliver_notices())]
        try:await asyncio.gather(*tasks)
        finally:
            for task in tasks:task.cancel()
            await asyncio.gather(*tasks,return_exceptions=True)
            await runner.cleanup()
            relay.db.close()

if __name__=='__main__':
    logging.basicConfig(level=logging.WARNING,format='%(asctime)s %(levelname)s %(message)s')
    try:asyncio.run(main())
    except KeyboardInterrupt:pass
    except Exception as error:
        logging.error('Общий бот остановлен: %s',type(error).__name__)
        raise SystemExit(1) from None
