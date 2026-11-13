'use strict';
const fs=require('node:fs'),path=require('node:path'),{createHash}=require('node:crypto');
function selectDigest(index, verified, candidate, readBlob) {
  if (verified?.verified!==true || candidate?.verified!==true ||
      JSON.stringify(verified)!==JSON.stringify(candidate) || verified.images?.length!==1 ||
      verified.images[0].platform!=='linux/amd64') throw Error('Release differs from the verified scanned candidate');
  if(index?.schemaVersion!==2 || index.manifests?.length!==1)throw Error('Expected one unambiguous exported OCI root');
  const root=index.manifests[0];
  if(!/^sha256:[a-f0-9]{64}$/.test(root.digest)||!Number.isSafeInteger(root.size)||root.size<1)throw Error('Invalid release digest');
  const bytes=readBlob(root.digest);
  if(bytes.length!==root.size||'sha256:'+createHash('sha256').update(bytes).digest('hex')!==root.digest)throw Error('Release root changed');
  return root.digest;
}
module.exports={selectDigest};
if(require.main===module){
 try{
  const [directory,verified,candidate]=process.argv.slice(2);
  const json=file=>JSON.parse(fs.readFileSync(file,'utf8'));
  console.log(selectDigest(json(path.join(directory,'index.json')),json(verified),json(candidate),digest=>fs.readFileSync(path.join(directory,'blobs/sha256',digest.slice(7)))));
 }catch(error){console.error(error.message);process.exitCode=1;}
}
