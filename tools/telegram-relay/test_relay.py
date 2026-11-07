import asyncio
import json
from pathlib import Path
import tempfile
import time
import unittest
import os
from aiohttp.test_utils import TestClient,TestServer
from relay import Relay

class RelayTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.folder=tempfile.TemporaryDirectory()
        self.calls=[]
        async def api(method,payload,files=()):
            self.calls.append((method,payload,files))
            return {'ok':True,'result':{'status':'member'} if method=='getChatMember' else True}
        self.relay=Relay(Path(self.folder.name)/'relay.sqlite',api)
        self.relay.telegram_online=True
        self.client=TestClient(TestServer(self.relay.app()))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        self.relay.db.close()
        self.folder.cleanup()

    async def pair(self,owner):
        response=await self.client.post('/pair/start')
        self.assertEqual(response.status,200)
        data=await response.json()
        header={'Authorization':'Bearer '+data['key']}
        code=data['link'].split('link_',1)[1]
        update={'update_id':owner,'message':{'chat':{'id':owner,'type':'private'},'from':{'id':owner,'username':'viewer'+str(owner)},'text':'/start link_'+code}}
        with self.relay.db:self.relay.route(update)
        state=await (await self.client.post('/pair/status',headers=header)).json()
        self.assertEqual(state['state'],'confirm')
        self.assertNotIn(data['key'],json.dumps(state))
        # Пока пользователь не подтвердил в приложении, доступа к API нет.
        denied=await self.client.post('/api/telegram/getMe',headers=header,json={})
        self.assertEqual(denied.status,401)
        confirmed=await self.client.post('/pair/confirm',headers=header)
        self.assertEqual((await confirmed.json())['owner_id'],owner)
        return header

    async def test_two_users_cannot_read_or_send_as_each_other(self):
        a,b=await self.pair(11),await self.pair(22)
        for ident,owner in [(100,11),(101,22)]:
            with self.relay.db:self.relay.route({'update_id':ident,'message':{'chat':{'id':owner,'type':'private'},'from':{'id':owner},'text':'/stats'}})
        result=await (await self.client.post('/api/telegram/getUpdates',headers=a,json={'offset':0})).json()
        self.assertEqual([row['update_id'] for row in result['result']],[100])
        result=await (await self.client.post('/api/telegram/getUpdates',headers=b,json={'offset':0})).json()
        self.assertEqual([row['update_id'] for row in result['result']],[101])
        denied=await self.client.post('/api/telegram/sendMessage',headers=a,json={'chat_id':22,'text':'wrong recipient'})
        self.assertEqual(denied.status,403)
        allowed=await self.client.post('/api/telegram/sendMessage',headers=a,json={'chat_id':11,'text':'own report'})
        self.assertTrue((await allowed.json())['ok'])
        forbidden=await self.client.post('/api/telegram/setWebhook',headers=a,json={'url':'https://example.invalid'})
        self.assertEqual(forbidden.status,403)
        self.assertEqual([call[0] for call in self.calls],['sendMessage'])

    async def test_revoke_and_repair_invalidate_old_application(self):
        old=await self.pair(11)
        new=await self.pair(11)
        denied=await self.client.post('/api/telegram/getMe',headers=old,json={})
        self.assertEqual(denied.status,401)
        self.assertEqual((await self.client.post('/pair/revoke',headers=new)).status,200)
        self.assertEqual((await self.client.post('/api/telegram/getMe',headers=new,json={})).status,401)

    async def test_callback_and_channel_access_are_owner_checked(self):
        a,b=await self.pair(11),await self.pair(22)
        update={'update_id':100,'callback_query':{'id':'private-callback','from':{'id':22},'message':{'chat':{'id':22,'type':'private'}},'data':'stats'}}
        with self.relay.db:self.relay.route(update)
        denied=await self.client.post('/api/telegram/answerCallbackQuery',headers=a,json={'callback_query_id':'private-callback'})
        self.assertEqual(denied.status,403)
        denied=await self.client.post('/api/telegram/sendMessage',headers=a,json={'chat_id':'@other_channel','text':'not an administrator'})
        self.assertEqual(denied.status,403)

    async def test_group_messages_are_never_routed(self):
        await self.pair(11)
        with self.relay.db:self.relay.route({'update_id':123,'message':{'chat':{'id':-42,'type':'group'},'from':{'id':11},'text':'/skip'}})
        self.assertEqual(self.relay.db.execute('SELECT COUNT(*) FROM updates').fetchone()[0],0)

    async def test_real_go_client_report_and_csv_bridge(self):
        headers=await self.pair(11)
        with self.relay.db:self.relay.route({'update_id':100,'message':{'message_id':1,'date':int(time.time()),'chat':{'id':11,'type':'private'},'from':{'id':11},'text':'/stats'}})
        environment=dict(os.environ,SONGREQUEST_TEST_RELAY_URL=str(self.client.make_url('/')).rstrip('/'),SONGREQUEST_TEST_RELAY_KEY=headers['Authorization'][7:])
        process=await asyncio.create_subprocess_exec('go','test','-count=1','-timeout','30s','-run','^TestRelayIntegration$','./internal/telegram',cwd=Path(__file__).resolve().parents[2],env=environment,stdout=asyncio.subprocess.PIPE,stderr=asyncio.subprocess.STDOUT)
        output,_=await asyncio.wait_for(process.communicate(),40)
        self.assertEqual(process.returncode,0,output.decode(errors='replace'))
        self.assertEqual([call[0] for call in self.calls],['sendMessage','sendDocument'])
        self.assertEqual(self.calls[-1][2][0][0],'document')
        self.assertEqual(self.calls[-1][2][0][2],b'user;song\nviewer;track\n')

if __name__=='__main__':unittest.main()
