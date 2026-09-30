const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
function setup(payload) {
  const nodes=new Map();const events={};const timers=[];const requests=[];
  const node=id=>{if(!nodes.has(id))nodes.set(id,{textContent:'',disabled:true,hidden:false,classList:{toggle(){}},addEventListener(name,fn){this[name]=fn;},setAttribute(){},style:{}});return nodes.get(id);};
  const context=vm.createContext({AbortController,crypto:{randomUUID:()=> 'unique-update-id'},localStorage:{getItem:()=>null,setItem(){},removeItem(){}},confirm:()=>true,window:{location:{href:'/admin/'}},document:{getElementById:node,querySelectorAll:()=>[],addEventListener:(name,fn)=>events[name]=fn},setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},setInterval(){},fetch:async(url,options)=>{requests.push({url,options});return {ok:true,json:async()=>structuredClone(payload)}}});
  for(const file of ['ui.js','system.js'])vm.runInContext(fs.readFileSync(__dirname+'/static/js/'+file,'utf8'),context);
  events.DOMContentLoaded();return {node,context,requests,timers};
}
const current={version:'v1.0.2+local',commit:'abcdef0',build_type:'local',repository:'zhangdailin/API-Console'};
const wait=()=>new Promise(resolve=>setImmediate(resolve));
test('failed discovery is never displayed as latest and cannot install',async()=>{const h=setup({current,available:false,warning:'GitHub offline',can_update:true});await wait();assert.equal(h.node('systemLatest').textContent,'未能检查');assert.match(h.node('systemCheckState').textContent,/offline/);assert(h.node('systemUpdate').disabled);});
test('unknown deployment capability prevents online upgrades while exposing release notes as text',async()=>{const h=setup({current,available:true,has_update:true,can_update:false,reason:'Docker',release:{tag_name:'v1.0.3',body:'<script>bad()</script>'}});await wait();assert(h.node('systemUpdate').disabled);assert.equal(h.node('systemReleaseNotes').textContent,'<script>bad()</script>');assert.match(h.node('systemCapability').textContent,/Docker/);});
test('running operations prevent another upgrade or rollback',async()=>{const h=setup({current,available:true,has_update:true,can_update:true,can_rollback:true,release:{tag_name:'v1.0.3'},operation:{phase:'restarting',message:'等待验证'}});await wait();assert(h.node('systemUpdate').disabled);assert(h.node('systemRollback').disabled);assert(h.timers.length>1);assert.equal(h.node('systemProgress').textContent,'等待验证');});
test('upgrade submission sends a stable idempotency key and exact release tag',async()=>{const h=setup({current,available:true,has_update:true,can_update:true,release:{tag_name:'v1.0.3'}});await wait();h.context.fetch=async(url,options)=>{h.requests.push({url,options});return{ok:true,json:async()=>({operation:{id:'op1',phase:'queued'}})}};await h.node('systemUpdate').click();const sent=h.requests.at(-1);assert.equal(sent.url,'/api/system/update');assert.equal(sent.options.headers['Idempotency-Key'],'unique-update-id');assert.deepEqual(JSON.parse(sent.options.body),{version:'v1.0.3'});});
