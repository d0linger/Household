// No runtime/build dependency: the catalogue deliberately uses JS-compatible TS.
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname,'..');
const target = path.join(root,'design-preview');
const source = fs.readFileSync(path.join(target,'designDirections.ts'),'utf8');
const escape = value => value.replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function inline(value) {
  return escape(value).replace(/`([^`]+)`/g,'<code>$1</code>').replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>').replace(/\[([^\]]+)\]\((https:\/\/[^ )]+|[^: )]+\.md(?:#[^ )]*)?)\)/g,(_,label,url) => `<span>${label}</span>`);
}
function markdown(markdown) {
  const lines = markdown.split(/\r?\n/), out = []; let list = '';
  const closeList = () => { if (list) {out.push(`</${list}>`);list='';} };
  for (let i=0;i<lines.length;i++) {
    const line=lines[i];
    if (!line.trim()) {closeList();continue;}
    const heading=line.match(/^(#{1,4}) (.+)$/);
    if (heading) {closeList();out.push(`<h${heading[1].length}>${inline(heading[2])}</h${heading[1].length}>`);continue;}
    if (line.startsWith('|') && lines[i+1]?.match(/^\|[\s:|-]+\|$/)) {
      closeList(); const cells=text => text.trim().slice(1,-1).split('|').map(cell=>inline(cell.trim()));
      out.push('<div class="guide-table" tabindex="0" role="region" aria-label="Designregeln als Tabelle"><table><thead><tr>'+cells(line).map(c=>`<th scope="col">${c}</th>`).join('')+'</tr></thead><tbody>');i++;
      while(lines[i+1]?.startsWith('|')) out.push('<tr>'+cells(lines[++i]).map(c=>`<td>${c}</td>`).join('')+'</tr>');
      out.push('</tbody></table></div>');continue;
    }
    const item=line.match(/^(?:([-*]) |(\d+)\. )(.+)$/);
    if(item){const kind=item[1]?'ul':'ol';if(list!==kind){closeList();out.push(`<${kind}>`);list=kind;}out.push(`<li>${inline(item[3])}</li>`);continue;}
    closeList();out.push(`<p>${inline(line)}</p>`);
  }
  closeList();return out.join('\n');
}
const guide=fs.readFileSync(path.join(root,'docs/design-preview-guideline.md'),'utf8');
const html=`<!doctype html><html lang="en" data-theme="light"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="description" content="Household design directions and comparable dialog guidelines."><title>Household: Design guideline</title><link rel="stylesheet" href="./laboratory.css"><link rel="stylesheet" href="./guideline.css"><link rel="icon" href="./mark.svg"></head><body><main class="guide"><a href="./">Zurück zum Designatelier</a>${markdown(guide)}<p><a href="/design-guideline.md" download>Download Markdown guideline</a></p></main></body></html>\n`;
function hsl(h,s,l){
  s/=100;l/=100;const a=s*Math.min(l,1-l);
  return '#'+[0,8,4].map(n=>{const k=(n+h/30)%12;return Math.round(255*(l-a*Math.max(-1,Math.min(k-3,9-k,1)))).toString(16).padStart(2,'0');}).join('');
}
function palette(design,dark){
  const h=design.hue,s=Math.min(75,Math.max(30,design.chroma*360));
  const colors=dark?{paper:hsl(h,18,13),field:hsl(h,20,20),ink:hsl(h,18,96),muted:hsl(h,18,79),rule:hsl(h,15,62),accent:hsl(h,s,75),'accent-ink':hsl(h,25,10),focus:hsl(h,s,80),error:'#ffb6c5',shadow:hsl(h,20,5)}:{paper:hsl(h,25,98),field:hsl(h,22,92),ink:hsl(h,25,13),muted:hsl(h,18,32),rule:hsl(h,20,43),accent:hsl(h,s,27),'accent-ink':hsl(h,20,99),focus:hsl(h,s,27),error:'#99233d',shadow:hsl(h,20,20)};
  return colors;
}
async function build(){
  const {designDirections}=await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
  if(designDirections.length!==100)throw new Error('Exactly100 designs required');
  let css='/* Hallmark · shape: app screen · theme: 100 authored design specifications · v2.0.0 */\n/* Critique: generated tokens express individually authored palettes and structural choices, never a three-family cycle. */\n';
  for(const d of designDirections){
    const gap={airy:'lg',balanced:'md',dense:'xs'}[d.density],pad={airy:'lg',balanced:'between',dense:'md'}[d.density];
    const radius={square:0,rounded:8,underline:0,pill:24}[d.control];
    css+=`.world[data-design="${d.id}"]{--world-body:var(--font-${d.font});--world-display:var(--font-${d.font});--world-weight:${d.weight};--world-radius:${d.radius}px;--world-control-radius:${radius}px;--world-padding:var(--space-${pad});--world-gap:var(--space-${gap});--world-title:${d.font==='condensed'?'1.75':'1.5'}rem;--world-tracking:${d.font==='mono'?'0':'-.02em'};--world-align:${d.alignment}}\n`;
    for(const mode of ['light','dark']){
      const colors=Object.entries(palette(d,mode==='dark'));
      css+=`:root[data-theme="${mode}"]{${colors.map(([name,value])=>`--${d.id}-${name}:${value}`).join(';')}}\n`;
      css+=`[data-theme="${mode}"] .world[data-design="${d.id}"]{${colors.map(([name])=>`--world-${name}:var(--${d.id}-${name})`).join(';')}}\n`;
    }
  }
  // Pink intentionally inherits the universal token palette through .world.
  css+='/* Pink Light and Pink Dark retain every direction\'s typography, navigation, structure and controls. Their color roles inherit tokens.css. */\n';
  const outputs={'designDirections.js':source,'directions.css':css,'guideline.html':html};
  for(const [name,content]of Object.entries(outputs)){
    if(process.argv.includes('--check')){if(!fs.existsSync(path.join(target,name))||fs.readFileSync(path.join(target,name),'utf8')!==content)throw new Error(`Generated ${name} is stale`);}
    else fs.writeFileSync(path.join(target,name),content);
  }
  console.log(process.argv.includes('--check')?'Design direction assets match their sources.':'Built100design definitions, independent color tokens and guideline.');
}
build().catch(error=>{console.error(error);process.exitCode=1;});
