'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),{createHash}=require('node:crypto'),YAML=require('yaml');
const {checkWorkflow}=require('./check-workflows.cjs'),{selectDigest}=require('./release-image.cjs');
const os=require('node:os'),{spawnSync}=require('node:child_process');
const root=path.resolve(__dirname,'..');
const release=()=>YAML.parse(fs.readFileSync(path.join(root,'.github/workflows/release.yml'),'utf8'));
test('production workflow keeps all quality gates read-only and grants registry writes only to serialized main publication',()=>{
 assert.deepEqual(checkWorkflow(YAML.stringify(release()),{release:true}),[]);
 assert.ok(checkWorkflow(YAML.stringify(release())).some(error=>error.includes('write permissions')));
});
for(const [name,mutate] of [
 ['PR trigger',doc=>doc.on.pull_request={}],['development publication',doc=>doc.on.push.branches.push('dev')],
 ['manual bypass',doc=>doc.on.workflow_dispatch={}],['global writer',doc=>doc.permissions.packages='write'],
 ['missing browser gate',doc=>doc.jobs.publish.needs=doc.jobs.publish.needs.filter(name=>name!=='browser')],
 ['skipped recovery',doc=>doc.jobs.recovery.if='false'],['failed candidate ignored',doc=>doc.jobs.candidate['continue-on-error']=true],
 ['wrong trusted ref',doc=>doc.jobs.publish.if="github.ref == 'refs/heads/dev'"],
 ['extra permissions',doc=>doc.jobs.publish.permissions.contents='write'],['cancelled publication',doc=>doc.jobs.publish.concurrency['cancel-in-progress']=true],
 ['inherited secrets',doc=>doc.jobs.quality.secrets='inherit'],['mutable action',doc=>doc.jobs.publish.steps[0].uses='actions/checkout@main'],
 ['persisted checkout credentials',doc=>doc.jobs.publish.steps[0].with['persist-credentials']=true],
 ['missing guard script',doc=>doc.jobs.publish.steps.at(-1).run='docker push bypass'],
 ['new writer job',doc=>doc.jobs.unchecked={...doc.jobs.publish}]
])test('release policy rejects '+name,()=>{const doc=release();mutate(doc);assert.notDeepEqual(checkWorkflow(YAML.stringify(doc),{release:true}),[]);});
test('reused checks cover dev and PRs and production calls without duplicate main push runs',()=>{
 for(const name of ['quality','browser','shopping-extensions','fuel-ocr-recovery','container']){
  const text=fs.readFileSync(path.join(root,'.github/workflows',name+'.yml'),'utf8'),doc=YAML.parse(text);
  assert.ok(Object.hasOwn(doc.on,'workflow_call'));assert.ok(Object.hasOwn(doc.on,'pull_request'));
  assert.deepEqual(doc.on.push.branches,['dev']);assert.deepEqual(checkWorkflow(text),[]);
 }
 const dependabot=YAML.parse(fs.readFileSync(path.join(root,'.github/dependabot.yml'),'utf8'));
 assert.ok(dependabot.updates.every(update=>update['target-branch']==='dev'));
 const promotion=YAML.parse(fs.readFileSync(path.join(root,'.github/workflows/promotion.yml'),'utf8'));
 assert.deepEqual(promotion.on,{pull_request:{branches:['main']}});
 assert.ok(promotion.jobs.PromotionSource);
});
function imageFixture(){
 const bytes=Buffer.from('{"schemaVersion":2,"fixture":"exact OCI bytes"}');
 const digest='sha256:'+createHash('sha256').update(bytes).digest('hex');
 const index={schemaVersion:2,manifests:[{digest,size:bytes.length}]};
 const report={verified:true,images:[{platform:'linux/amd64',digest}]};
 return {bytes,digest,index,report};
}
test('promotion preserves the exact verified exported root digest',()=>{
 const f=imageFixture();assert.equal(selectDigest(f.index,f.report,structuredClone(f.report),()=>f.bytes),f.digest);
});
for(const [name,change] of [
 ['modified bytes',f=>{f.bytes=Buffer.from('changed');}],['ambiguous root',f=>{f.index.manifests.push({...f.index.manifests[0]});}],
 ['different scan candidate',f=>{f.candidate.images[0].digest='sha256:'+'f'.repeat(64);}],
 ['unverified report',f=>{f.report.verified=false;}],['unsupported platform',f=>{f.report.images[0].platform='linux/arm64';f.candidate=structuredClone(f.report);}],
 ['malformed digest',f=>{f.index.manifests[0].digest='../../secret';}]
])test('image promotion rejects '+name,()=>{const f=imageFixture();f.candidate=structuredClone(f.report);change(f);assert.throws(()=>selectDigest(f.index,f.report,f.candidate,()=>f.bytes));});

for(const failAt of [0,1,2,3,4,5])test('publication shell '+(failAt?'stops at stale-ref guard '+failAt:'promotes the verified digest and cleans credentials'),t=>{
 const temporary=fs.mkdtempSync(path.join(os.tmpdir(),'household-publish-test-'));
 t.after(()=>{assert.equal(path.dirname(temporary),os.tmpdir());assert.ok(path.basename(temporary).startsWith('household-publish-test-'));fs.rmSync(temporary,{recursive:true,force:true});});
 for(const name of ['bin','runtime','candidate'])fs.mkdirSync(path.join(temporary,name));
 // A real empty tar is sufficient here: graph validation has separate tests.
 fs.writeFileSync(path.join(temporary,'candidate/household.oci.tar'),Buffer.alloc(10240));
 const raw='{"fixture":true}',digest='sha256:'+createHash('sha256').update(raw).digest('hex');
 const shim=(name,text)=>fs.writeFileSync(path.join(temporary,'bin',name),'#!/usr/bin/env bash\nset -euo pipefail\n'+text,{mode:0o700});
 shim('node',`case "$1" in
 scripts/release-guard.cjs)
  counter=0; if test -f guard-count; then counter="$(cat guard-count)"; fi
  counter=$((counter+1)); printf '%s' "$counter" > guard-count
  if test "$counter" = "$FAIL_AT"; then exit 1; fi ;;
 scripts/check-oci.cjs) printf '{}' > "$3" ;;
 scripts/release-image.cjs) printf '%s\\n' "$EXPECTED_DIGEST" ;;
 *) exit 91 ;;
esac\n`);
 shim('skopeo',`if test "$1" = --command-timeout; then shift 2; fi
case "$1" in
 login) cat > /dev/null ;;
 inspect) printf '%s' '${raw}' ;;
 copy)
  shift; destination=''; digestfile=''
  while test "$#" -gt 0; do
   case "$1" in --digestfile) digestfile="$2"; shift;; docker://*) destination="$1";; esac
   shift
  done
  printf '%s\\n' "$destination" >> registry-writes
  printf '%s\\n' "$EXPECTED_DIGEST" > "$digestfile" ;;
 *) exit 92 ;;
esac\n`);
 const bash=process.platform==='win32'?'C:/Program Files/Git/bin/bash.exe':'bash';
 const unix=value=>value.replaceAll('\\','/').replace(/^([A-Za-z]):/,(_,drive)=>'/'+drive.toLowerCase());
 const result=spawnSync(bash,[path.join(root,'scripts/publish-production.sh').replaceAll('\\','/')],{cwd:temporary,encoding:'utf8',timeout:15000,windowsHide:true,env:{...process.env,PATH:unix(path.join(temporary,'bin'))+':/usr/bin:/bin',RUNNER_TEMP:'./runtime',CANDIDATE_DIRECTORY:'./candidate',GH_TOKEN:'synthetic-token-for-no-network-test',GITHUB_ACTOR:'fixture',GITHUB_RUN_ID:'42',GITHUB_RUN_ATTEMPT:'1',GITHUB_SHA:'a'.repeat(40),GITHUB_STEP_SUMMARY:'./summary',FAIL_AT:String(failAt),EXPECTED_DIGEST:digest}});
 assert.equal(result.error,undefined);assert.equal(result.status,failAt?1:0,result.stderr);
 const writes=fs.existsSync(path.join(temporary,'registry-writes'))?fs.readFileSync(path.join(temporary,'registry-writes'),'utf8').trim().split('\n'):[];
 const expected=['build-42-1','sha-'+'a'.repeat(40),'production','latest'].map(tag=>'docker://ghcr.io/d0linger/household:'+tag);
 assert.deepEqual(writes,expected.slice(0,failAt?Math.max(0,failAt-2):4));
 assert.deepEqual(fs.readdirSync(path.join(temporary,'runtime')),[],'temporary auth/layout must be removed on success and failure');
});
