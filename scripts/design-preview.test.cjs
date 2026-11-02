const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const {createServer} = require('./serve-design-preview.cjs');

const root = path.resolve(__dirname, '..');
const preview = path.join(root, 'design-preview');
const read = name => fs.readFileSync(path.join(preview, name), 'utf8');
const importSource = source => import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const configPromise = importSource(read('designDirections.ts'));
const fixturesPromise = importSource(read('dialog-fixtures.js'));
const rendererPromise = importSource(read('dialog-components.js'));
const structuralFields = ['font','layout','treatment','radius','weight','density','alignment','control','navigation'];

function attrs(source) {
  const values = {};
  for (const match of source.matchAll(/([:\w-]+)(?:\s*=\s*"([^"]*)")?/g)) values[match[1]] = match[2] === undefined ? true : match[2];
  return values;
}
function controls(markup) {
  const result = [];
  for (const match of markup.matchAll(/<input\b([^>]*)>|<select\b([^>]*)>([\s\S]*?)<\/select>|<textarea\b([^>]*)>([\s\S]*?)<\/textarea>/g)) {
    const tag = match[1] !== undefined ? 'input' : match[2] !== undefined ? 'select' : 'textarea';
    const attributes = attrs(match[1] ?? match[2] ?? match[4]);
    if (!attributes.name) continue;
    let value = attributes.value;
    let options;
    if (tag === 'textarea') value = match[5];
    if (tag === 'select') {
      options = [...match[3].matchAll(/<option\b([^>]*)>([\s\S]*?)<\/option>/g)].map(option => ({...attrs(option[1]),label:option[2]}));
      value = (options.find(option => option.selected) || options[0])?.value;
    }
    if (attributes.type === 'checkbox') value = attributes.checked === true;
    result.push({tag,...attributes,value,options});
  }
  return result;
}

test('catalogue contains 100 individually named and structurally distinct design directions', async () => {
  const {designDirections} = await configPromise;
  assert.equal(designDirections.length,100);
  assert.deepEqual(designDirections.map(d => d.id),Array.from({length:100},(_,i) => 'DS-' + String(i+1).padStart(3,'0')));
  assert.equal(new Set(designDirections.map(d => d.name)).size,100);
  const signatures = designDirections.map(d => structuralFields.map(key => String(d[key])).join('/'));
  assert.equal(new Set(signatures).size,100,'a new name or hue alone is not a new design');
  for (const d of designDirections) {
    for (const key of ['id','name','group','thesis','signature',...structuralFields.filter(k => !['radius','weight'].includes(k))]) {
      assert.equal(typeof d[key],'string',d.id + '/' + key);
      assert.ok(d[key].trim(),d.id + '/' + key);
      assert.doesNotMatch(d[key],/\b(undefined|null|TODO|FILL ME)\b/);
    }
    assert.ok(Number.isFinite(d.hue) && d.hue >= 0 && d.hue < 360,d.id + '/hue');
    assert.ok(Number.isFinite(d.chroma) && d.chroma >= 0.04 && d.chroma <= 0.2,d.id + '/chroma');
    assert.ok(Number.isInteger(d.radius) && d.radius >= 0 && d.radius <= 28,d.id + '/radius');
    assert.ok([400,600,700,800].includes(d.weight),d.id + '/weight');
  }
});

test('design variety extends beyond recoloring the original three families', async () => {
  const {designDirections} = await configPromise;
  for (const [key,minimum] of [['group',10],['font',6],['layout',8],['treatment',12],['navigation',3],['hue',20]]) {
    assert.ok(new Set(designDirections.map(d => d[key])).size >= minimum,key + ' diversity');
  }
  assert.ok(designDirections.every(d => !Object.hasOwn(d,'category')),'all designs apply to every dialog');
});

test('ten common dialogs have unique names and native-valid fixture values', async () => {
  const {dialogs} = await fixturesPromise;
  assert.deepEqual(dialogs.map(d => d.id),['shopping','chores','inventory','finance','recipes','mealplan','fuel','meters','documents','smart-home']);
  for (const d of dialogs) {
    assert.ok(d.fields.length >= 4 && d.fields.length <= 6,d.id);
    assert.equal(new Set(d.fields.map(f => f.name)).size,d.fields.length,d.id);
    for (const key of ['label','title','description','submitLabel','note']) assert.ok(typeof d[key] === 'string' && d[key].trim(),d.id + '/' + key);
    assert.ok(d.summary.length >= 1 && d.summary.every(row => row.label && row.value),d.id + '/summary');
    assert.match(d.note,/Beispieldaten|Simulierte/);
    for (const field of d.fields) {
      assert.match(field.name,/^[a-zA-Z][a-zA-Z0-9]*$/);
      assert.ok(['text','number','select','date','textarea','checkbox','range'].includes(field.type));
      assert.equal(typeof field.value,field.type === 'checkbox' ? 'boolean' : 'string',d.id + '/' + field.name);
      if (field.required) assert.ok(String(field.value).trim());
      if (['number','range'].includes(field.type)) {
        const value = Number(field.value);
        assert.ok(Number.isFinite(value));
        assert.ok(value >= field.min && value <= field.max,d.id + '/' + field.name);
        const step = (value - field.min) / field.step;
        assert.ok(Math.abs(step - Math.round(step)) < 0.000001,d.id + '/' + field.name + '/step');
      }
      if (field.type === 'date' && field.value) {
        assert.match(field.value,/^\d{4}-\d{2}-\d{2}$/);
        assert.equal(new Date(field.value).toISOString().slice(0,10),field.value);
      }
      if (field.type === 'select') {
        assert.ok(field.options.some(option => option.value === field.value),d.id + '/' + field.name);
        assert.equal(new Set(field.options.map(option => option.value)).size,field.options.length);
        assert.ok(field.options.every(option => typeof option.label === 'string' && option.label.trim()));
      }
    }
  }
});

test('generated design catalogue remains exactly equal to its canonical source', () => {
  assert.equal(read('designDirections.js'),read('designDirections.ts'));
});

test('generated design CSS includes every identity while retaining all four global modes', async () => {
  const {designDirections} = await configPromise;
  const css = read('directions.css');
  for (const design of designDirections) assert.ok(css.includes(design.id),design.id + ' CSS');
  const tokens = read('tokens.css');
  for (const theme of ['light','dark','pink-light','pink-dark']) {
    const block = tokens.match(new RegExp('\\[data-theme="' + theme + '"\\]\\s*\\{([^}]+)\\}'));
    assert.ok(block,theme);
    for (const role of ['paper','paper-2','paper-3','ink','muted','rule','accent','accent-ink','focus']) assert.match(block[1],new RegExp('--color-' + role + '\\s*:'),theme + '/' + role);
  }
  assert.match(css,/data-theme="light"/);
  assert.match(css,/data-theme="dark"/);
  assert.doesNotMatch(css,/\[data-theme="pink-(?:light|dark)"\][^{]*\{[^}]*--color-/,'individual palettes must not replace the universal pink modes');
  assert.match(css,/--(?:[^:;{}]*font[^:;{}]*|world-body|world-display)\s*:/);
});

test('all 100 designs render all ten dialogs with identical valid fixture content', async () => {
  const [{designDirections},{dialogs},{dialogMarkup,escapeHTML}] = await Promise.all([configPromise,fixturesPromise,rendererPromise]);
  const allIds = new Set();
  for (const fixture of dialogs) {
    let reference;
    for (const design of designDirections) {
      const markup = dialogMarkup(design,fixture,'test');
      const ids = [...markup.matchAll(/\sid="([^"]+)"/g)].map(match => match[1]);
      assert.equal(new Set(ids).size,ids.length,design.id + '/' + fixture.id + ' duplicate IDs');
      for (const id of ids) { assert.ok(!allIds.has(id),'cross-dialog ID collision: ' + id); allIds.add(id); }
      for (const match of markup.matchAll(/\sfor="([^"]+)"/g)) assert.ok(ids.includes(match[1]),'orphan label: ' + match[1]);
      const rendered = controls(markup);
      assert.equal(rendered.length,fixture.fields.length,design.id + '/' + fixture.id);
      for (const field of fixture.fields) {
        const control = rendered.find(c => c.name === field.name);
        assert.ok(control,design.id + '/' + fixture.id + '/' + field.name);
        assert.equal(control.tag,['select','textarea'].includes(field.type) ? field.type : 'input');
        if (control.tag === 'input') assert.equal(control.type,field.type);
        assert.equal(control.value,typeof field.value === 'string' ? escapeHTML(field.value) : field.value,design.id + '/' + fixture.id + '/' + field.name);
        assert.equal(Object.hasOwn(control,'required'),Boolean(field.required));
        for (const key of ['min','max','step']) if (field[key] !== undefined) assert.equal(control[key],String(field[key]),field.name + '/' + key);
        assert.ok(control.id && ids.includes(control.id));
        assert.ok(markup.includes('for="' + control.id + '"'),'control lacks explicit label');
      }
      const semantic = rendered.map(control => Object.fromEntries(['tag','name','type','value','options','required','min','max','step'].map(key => [key,control[key]])));
      if (!reference) reference = semantic;
      else assert.deepEqual(semantic,reference,'a design changed the comparison data: ' + design.id + '/' + fixture.id);
      assert.ok(markup.includes(escapeHTML(fixture.title)));
      assert.ok(markup.includes(escapeHTML(fixture.note)));
      assert.doesNotMatch(markup,/\son(?:click|change|input|error|submit)\s*=/i);
      assert.doesNotMatch(markup,/<script\b|<iframe\b/i);
      const comparison = dialogMarkup(design,fixture,'compare');
      for (const match of comparison.matchAll(/\sid="([^"]+)"/g)) assert.ok(!allIds.has(match[1]),'compare ID collision');
    }
  }
});

test('HTML escaping protects malicious dialog text, field values and attributes', async () => {
  const [{designDirections},{dialogs},{dialogMarkup,escapeHTML}] = await Promise.all([configPromise,fixturesPromise,rendererPromise]);
  const payload = '<img src=x onerror="alert(1)"> & ' + "'quoted'";
  assert.equal(escapeHTML(payload),'&lt;img src=x onerror=&quot;alert(1)&quot;&gt; &amp; &#39;quoted&#39;');
  const fixture = {...dialogs[0],title:payload,description:payload,note:payload,summary:[{label:payload,value:payload}],fields:[{name:payload,label:payload,type:'text',value:payload,required:true}]};
  const markup = dialogMarkup(designDirections[0],fixture,'escape');
  assert.doesNotMatch(markup,/<img\b|onerror="/i);
  assert.ok(markup.includes('value="' + escapeHTML(payload) + '"'));
  assert.ok(markup.includes(escapeHTML(payload)));
  assert.equal(escapeHTML(0),'0');
});

test('shared edited values can be rendered equally into different designs', async () => {
  const [{designDirections},{dialogs},{dialogMarkup}] = await Promise.all([configPromise,fixturesPromise,rendererPromise]);
  const fixture = dialogs.find(d => d.id === 'shopping');
  const edited = {item:'Rote Linsen',quantity:'7',unit:'piece',shop:'market',note:'Ein gemeinsamer Vergleichswert'};
  for (const design of [designDirections[0],designDirections[49],designDirections[99]]) {
    const rendered = controls(dialogMarkup(design,fixture,'edited',edited));
    for (const [name,value] of Object.entries(edited)) assert.equal(rendered.find(c => c.name === name).value,value,design.id + '/' + name);
  }
});

test('runtime assets stay local and expose focus and reduced-motion rules', () => {
  for (const name of ['tokens.css','base.css','laboratory.css','world.css','directions.css','guideline.css']) {
    const css = read(name);
    assert.doesNotMatch(css,/(?:url\(|@import\s+)[\s"']*(?:https?:|\/\/)/i,name);
    assert.doesNotMatch(css,/transition\s*:\s*all\b/i,name);
  }
  assert.match(read('base.css'),/prefers-reduced-motion/);
  assert.match(read('base.css'),/:focus-visible/);
  const html = read('index.html');
  assert.match(html,/<html\b[^>]*lang="de"/);
  assert.match(html,/Beispieldaten/);
  assert.match(html,/<noscript>/);
  for (const match of html.matchAll(/<(script|link|img)\b[^>]*\b(?:src|href)="([^"]+)"/g)) {
    assert.doesNotMatch(match[2],/^(?:https?:|\/\/)/,match[0]);
    assert.ok(fs.existsSync(path.join(preview,match[2].replace(/^\.\//,''))),match[2]);
  }
});

function request(server,pathname,options={}) {
  return new Promise((resolve,reject) => {
    const req = http.request({hostname:'127.0.0.1',port:server.address().port,path:pathname,method:options.method||'GET',headers:{Host:options.host||'localhost:' + server.address().port},agent:false},res => {
      const chunks = [];
      res.on('data',chunk=>chunks.push(chunk));
      res.on('end',()=>resolve({status:res.statusCode,headers:res.headers,body:Buffer.concat(chunks)}));
      res.on('error',reject);
    });
    req.setTimeout(5000,()=>req.destroy(new Error('Preview request timed out')));
    req.on('error',reject);req.end();
  });
}

test('standalone server exposes only the read-only laboratory on loopback', async t => {
  const server = createServer();
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
  t.after(async()=>{server.closeAllConnections();await new Promise(resolve=>server.close(resolve));});
  assert.equal(server.address().address,'127.0.0.1');
  await t.test('root and slashless route redirect to the canonical laboratory',async()=>{
    for(const route of ['/','/design-preview']){const r=await request(server,route);assert.equal(r.status,302);assert.equal(r.headers.location,'/design-preview/');}
  });
  await t.test('entry, design catalogue, fixtures and generated CSS return exact local files',async()=>{
    for(const [route,file,type] of [['/design-preview/','index.html','text/html'],['/design-preview/designDirections.js','designDirections.js','text/javascript'],['/design-preview/dialog-fixtures.js','dialog-fixtures.js','text/javascript'],['/design-preview/directions.css','directions.css','text/css']]){
      const r=await request(server,route);assert.equal(r.status,200);assert.equal(r.body.toString(),read(file));assert.ok(r.headers['content-type'].startsWith(type));
    }
  });
  await t.test('HEAD returns headers without a response body',async()=>{
    const r=await request(server,'/design-preview/index.html',{method:'HEAD'});assert.equal(r.status,200);assert.equal(r.body.length,0);assert.equal(Number(r.headers['content-length']),Buffer.byteLength(read('index.html')));
  });
  await t.test('secrets, repository files and traversal candidates are never served',async()=>{
    for(const route of ['/.env','/design-preview/.env','/design-preview/../.env','/design-preview/%2e%2e/.env','/design-preview/%2e%2e%2f.env','/design-preview/..%5c.env','/design-preview/%2e%2e/%2e%2e/PRODUCT.md','/design-preview/.local/secrets/bootstrap_password','/design-preview/unknown.js']){
      const r=await request(server,route);assert.equal(r.status,404,route);assert.equal(r.body.toString(),'Not found');
    }
  });
  await t.test('mutations and non-local Host values are refused',async()=>{
    for(const method of ['POST','PUT','DELETE','OPTIONS']){const r=await request(server,'/design-preview/',{method});assert.equal(r.status,405);assert.equal(r.headers.allow,'GET, HEAD');}
    for(const host of ['example.com','localhost.example.com','127.0.0.1.example.com','evil.localhost','localhost@evil.test']){const r=await request(server,'/design-preview/',{host});assert.equal(r.status,403,host);}
  });
  await t.test('responses carry restrictive CSP, no-store and browser capability policies',async()=>{
    const r=await request(server,'/design-preview/');
    assert.equal(r.headers['cache-control'],'no-store');assert.equal(r.headers['x-content-type-options'],'nosniff');assert.equal(r.headers['referrer-policy'],'no-referrer');
    const csp=r.headers['content-security-policy'];
    for(const directive of ["default-src 'none'","script-src 'self'","style-src 'self'","frame-ancestors 'none'","form-action 'none'","base-uri 'none'"])assert.ok(csp.includes(directive),directive);
    assert.doesNotMatch(csp,/unsafe-inline|unsafe-eval|https?:/);
    assert.match(r.headers['permissions-policy'],/camera=\(\).*microphone=\(\).*geolocation=\(\)/);
  });
  await t.test('guideline download is the requested public design document',async()=>{
    const r=await request(server,'/design-guideline.md');assert.equal(r.status,200);assert.ok(r.headers['content-type'].startsWith('text/markdown'));assert.equal(r.body.toString(),fs.readFileSync(path.join(root,'docs/design-preview-guideline.md'),'utf8'));
  });
});
