const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('./test-support.cjs');
function element(tag='div'){
 return {tag,children:[],textContent:'',classList:{add(){},remove(){},toggle(){}},appendChild(n){this.children.push(n);return n},replaceChildren(){this.children=[]},listeners:{},addEventListener(event,fn){this.listeners[event]=fn},setAttribute(){}};
}
function load(){
 const context=vm.createContext({console,URLSearchParams,document:{readyState:'loading',addEventListener(){}},window:{}});
 context.document.createElement=element;
 let src=fs.readFileSync(path.join(__dirname,'static/js/logs.js'),'utf8');
 src=src.replace(/\}\)\(\);\s*$/,'globalThis.review={sectionLabel,renderBundle};})();');
 vm.runInContext(src,context);return context.review;
}
const allText=n=>[n.textContent,...n.children.map(allText)].join(' ');
test('diagnostic attempt labels distinguish request, response, status and read failure',()=>{
 const api=load();
 for(const [name,label] of [['request.json','请求'],['response.txt','响应内容'],['result.json','HTTP 状态'],['read_error.json','响应读取错误']]){
  assert.equal(api.sectionLabel({name:'upstream_002_'+name}),'上游尝试 2 · '+label);
 }
 // The remaining numbered sections keep their own labels...
 assert.equal(api.sectionLabel({name:'3_upstream_request.json'}),'3 · 上游请求');
 assert.equal(api.sectionLabel({name:'5_client_sse.jsonl'}),'5 · 返回客户端 SSE');
 // ...and the upstream SSE capture that no longer exists has no stale label.
 assert.equal(api.sectionLabel({name:'4_upstream_sse.jsonl'}),'4_upstream_sse.jsonl');
});
test('bundle renders both attempts without hiding truncation or interpreting response markup',()=>{
 const api=load(),container=element();
 api.renderBundle(container,{bytes:200,duration_ms:31,truncated:true,sections:[
  {name:'upstream_001_request.json',payload:'first',bytes:5},
  {name:'upstream_001_read_error.json',payload:'read failed',bytes:11},
  {name:'upstream_002_response.txt',payload:'<script>example</script>',bytes:24,truncated:true},
 ]},'24 小时');
 assert.match(allText(container),/上游尝试 1/);assert.match(allText(container),/上游尝试 2/);
 assert.match(allText(container),/已截断/);assert.match(allText(container),/请求耗时 31 ms/);
 const details=container.children.filter(n=>n.tag==='details');assert.equal(details[1].open,true);
 assert.equal(details[2].children[1].textContent,''); details[2].open=true; details[2].listeners.toggle(); assert.equal(details[2].children[1].textContent,'<script>example</script>'); assert.match(allText(container),/下载完整诊断/);
});

test('latency diagnostics show zero, reused connections and upstream clock separately',()=>{
 const api=load(),container=element();
 api.renderBundle(container,{sections:[{name:'upstream_002_latency.json',payload:JSON.stringify({connection_reused:true,first_sse_ms:0,first_text_ms:838,upstream_firstTokenDuration:700,model_key:'qfmodel',httpdns_ip:''}),bytes:100}]},'');
 const text=allText(container);
 assert.match(text,/复用连接：是/);assert.match(text,/首条 SSE：0 ms/);assert.match(text,/首个正文：838 ms/);assert.match(text,/上游报告首 Token：700 ms/);assert.match(text,/实际模型路由：qfmodel/);assert.match(text,/HTTPDNS 请求头：未记录/);
});
