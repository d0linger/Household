// Standalone loopback-only design laboratory. Never serves the project root.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname,'..');
const types={'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css; charset=utf-8','.svg':'image/svg+xml','.md':'text/markdown; charset=utf-8','.ts':'text/plain; charset=utf-8'};
const names=['index.html','guideline.html','guideline.css','mark.svg','tokens.css','base.css','laboratory.css','world.css','directions.css','dialog-components.js','dialog-fixtures.js','direction-app.js','designDirections.js','designDirections.ts'];
function createServer(){
  return http.createServer((req,res)=>{
    res.setHeader('Content-Security-Policy',"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'; object-src 'none'");
    res.setHeader('X-Content-Type-Options','nosniff');res.setHeader('Referrer-Policy','no-referrer');res.setHeader('Cache-Control','no-store');res.setHeader('Permissions-Policy','camera=(), microphone=(), geolocation=()');
    const deny=(status,message)=>{res.writeHead(status,{'Content-Type':'text/plain; charset=utf-8'});res.end(message);};
    if(!/^((localhost|127\.0\.0\.1)(:\d+)?)$/.test(req.headers.host||'')) return deny(403,'Local host required');
    if(!['GET','HEAD'].includes(req.method)){res.setHeader('Allow','GET, HEAD');return deny(405,'Read-only preview');}
    let pathname;try{pathname=new URL(req.url,'http://localhost').pathname;}catch{return deny(400,'Invalid URL');}
    if(pathname==='/' || pathname==='/design-preview'){res.writeHead(302,{Location:'/design-preview/'});return res.end();}
    let file;
    if(pathname==='/design-guideline.md')file=path.join(root,'docs/design-preview-guideline.md');
    else{
      if(!pathname.startsWith('/design-preview/'))return deny(404,'Not found');
      const name=pathname.slice('/design-preview/'.length)||'index.html';
      if(!names.includes(name))return deny(404,'Not found');
      file=path.join(root,'design-preview',name);
    }
    fs.readFile(file,(error,data)=>{if(error)return deny(404,'Not found');res.writeHead(200,{'Content-Type':types[path.extname(file)],'Content-Length':data.length});res.end(req.method==='HEAD'?undefined:data);});
  });
}
if(require.main===module){
  const port=Number(process.env.HOUSEHOLD_DESIGN_PORT||8102);
  if(!Number.isInteger(port)||port<1024||port>65535)throw new Error('Invalid HOUSEHOLD_DESIGN_PORT');
  const server=createServer();server.requestTimeout=10000;server.headersTimeout=10000;server.keepAliveTimeout=3000;server.maxConnections=32;
  server.on('error',error=>{console.error(error.message);process.exitCode=1;});
  server.listen(port,'127.0.0.1',()=>console.log(`Household design preview: http://localhost:${port}/design-preview/`));
  const stop=()=>server.close(()=>process.exit(0));process.on('SIGINT',stop);process.on('SIGTERM',stop);
}
module.exports={createServer};
