const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const fs=require('node:fs/promises');
const path=require('node:path');
const assert=require('node:assert/strict');
const {pathToFileURL}=require('node:url');
const root=path.resolve(__dirname,'..');
const round=process.argv.includes('--confirm')?'confirm':process.argv.includes('--functional')?'functional':'initial';
const out=path.join(root,'outputs/design-directions-review',round+'-'+new Date().toISOString().replace(/[:.]/g,'-'));
const url='http://localhost:8102/design-preview/';
const themes=['light','dark','pink-light','pink-dark'];
const report={round,checks:[],a11y:[],contrast:[],screens:[],errors:[],overflow:[],network:[],functionalIssues:[],thumbnails:[]};
const check=(name,condition)=>{assert.ok(condition,name);report.checks.push(name);};
async function main(){
  await fs.mkdir(out,{recursive:true});
  const browser=await chromium.launch({channel:'msedge',headless:true,args:['--disable-extensions']});
  const deadline=setTimeout(()=>browser.close().catch(()=>{}),240000);deadline.unref();
  try{
    const context=await browser.newContext({viewport:{width:1440,height:1000},acceptDownloads:true});
    context.setDefaultTimeout(10000);
    const page=await context.newPage();
    page.on('pageerror',error=>report.errors.push(error.message));
    page.on('request',request=>{if(!request.url().startsWith('http://localhost:8102/')&&!request.url().startsWith('blob:'))report.network.push(request.url());});
    await page.goto(url);await page.locator('.design-card').last().waitFor({state:'attached'});
    check('100 authored directions rendered',await page.locator('.design-card').count()===100);
    const identities=await page.locator('.design-card').evaluateAll(nodes=>nodes.map(node=>node.dataset.card));
    check('100 stable distinct identities',new Set(identities).size===100);
    const fixtures=await page.evaluate(async()=> (await import('./dialog-fixtures.js')).dialogs);
    const groups=await page.locator('#group option').evaluateAll(nodes=>nodes.map(node=>node.value).filter(value=>value!=='all'));
    for(const group of groups){
      await page.selectOption('#group',group);
      check('Group filter '+group,await page.locator('.design-card').count()>0&&await page.locator('.design-card .design-meta p').evaluateAll((nodes,expected)=>nodes.every(node=>node.textContent.includes(expected)),group));
    }
    await page.click('#reset-filters');await page.fill('#query','DS-043');await page.waitForFunction(()=>document.querySelectorAll('.design-card').length===1);
    check('Stable-ID search',await page.locator('.design-card').count()===1);
    await page.click('[data-favorite="DS-043"]');await page.selectOption('#theme','pink-dark');
    await page.reload();await page.locator('.design-card').waitFor();
    check('Favorite and theme persisted',await page.locator('[data-favorite="DS-043"]').getAttribute('aria-pressed')==='true'&&await page.locator('html').getAttribute('data-theme')==='pink-dark');
    await page.click('#reset-filters');await page.check('#favorites-only');
    check('Favorites filter',await page.locator('.design-card').count()===1);await page.uncheck('#favorites-only');
    await page.fill('#query','nothing-by-this-name');await page.locator('#empty').waitFor({state:'visible'});check('Empty search state',await page.locator('#empty').isVisible());await page.click('#empty-reset');
    for(const id of ['DS-001','DS-027','DS-061','DS-092'])await page.click(`[data-shortlist="${id}"]`);
    check('Shortlist bounded to four',await page.locator('[data-shortlist="DS-003"]').isDisabled()&&await page.locator('#shortlist li').count()===4);
    await page.click('[data-remove="DS-092"]');check('Remove frees comparison slot',!(await page.locator('[data-shortlist="DS-003"]').isDisabled()));await page.click('[data-shortlist="DS-092"]');
    await page.click('#compare');check('Four actual comparison dialogs',await page.locator('#comparison-grid .world').count()===4);
    check('Modal initial close focus',await page.locator('#close-compare').evaluate(node=>node===document.activeElement));
    for(let index=0;index<8;index++){await page.keyboard.press('Tab');check('Modal keyboard focus contained '+index,await page.locator('#compare-dialog').evaluate(node=>node.contains(document.activeElement)));}
    const first=page.locator('#comparison-grid .world').first();
    const second=page.locator('#comparison-grid .world').nth(1);
    for(const fixture of fixtures){
      await page.selectOption('#compare-fixture',fixture.id);
      const field=fixture.fields.find(item=>item.type==='text'||item.type==='textarea')||fixture.fields.find(item=>item.type==='range'||item.type==='number');
      const input=first.locator(`[name="${field.name}"]`);
      let changed;
      if(field.type==='range'){await input.focus();await input.press('ArrowRight');changed=await input.inputValue();}
      else{changed=field.type==='number'?String(Number(field.value)+field.step):'Gemeinsamer Test '+fixture.id;await input.fill(changed);}
      const values=await page.locator(`#comparison-grid [name="${field.name}"]`).evaluateAll(nodes=>nodes.map(node=>node.value));
      check(fixture.id+': identical synchronized edits',values.length===4&&values.every(value=>value===changed));
      if(field.type==='range')check('Smart-home range output updates',await first.locator('output').textContent()===changed);
      await first.locator('[type="submit"]').click();check(fixture.id+': valid preview and explicit simulation',await first.locator('.world-review').isVisible()&&/Keine Haushaltsdaten gespeichert/.test(await first.locator('.world-status').textContent()));
      await first.locator('[data-back]').click();check(fixture.id+': return-to-edit focus',await first.locator(`[name="${fixture.fields[0].name}"]`).evaluate(node=>node===document.activeElement));
      await page.click('#reset-dialog');
      const restored=await first.locator(`[name="${field.name}"]`).inputValue();check(fixture.id+': common reset',restored===field.value);
    }
    await page.selectOption('#compare-fixture','shopping');
    await first.locator('[name="item"]').fill('');await first.locator('[type="submit"]').click();
    report.validationState=await first.evaluate(node=>({active:document.activeElement?.outerHTML,item:node.querySelector('[name="item"]').outerHTML,status:node.querySelector('.world-status').textContent}));
    if(!report.validationState.status){report.functionalIssues.push('Required-field pointer submission did not trigger validation status after focusout.');await first.locator('[type="submit"]').press('Enter');}
    check('Required validation focuses field',await first.locator('[name="item"]').getAttribute('aria-invalid')==='true'&&await first.locator('[name="item"]').evaluate(node=>node===document.activeElement));
    check('Required validation has readable inline message',/Pflichtfeld/.test(await first.locator('[data-field="item"] .field-error').textContent()));
    await first.locator('[name="item"]').fill('Linsen');await first.locator('[name="quantity"]').fill('100');await first.locator('[type="submit"]').click();
    if(!(await first.locator('.world-status').textContent()))await first.locator('[type="submit"]').press('Enter');
    check('Native numeric bounds enforced',await first.locator('[name="quantity"]').getAttribute('aria-invalid')==='true');
    await page.click('#reset-dialog');await page.uncheck('#sync-inputs');await first.locator('[name="item"]').fill('Nur erster Entwurf');
    check('Synchronization can be disabled',await second.locator('[name="item"]').inputValue()==='Hafermilch');await page.check('#sync-inputs');
    check('Re-enabling synchronizes from first design',await second.locator('[name="item"]').inputValue()==='Nur erster Entwurf');
    await page.selectOption('#compare-theme','pink-light');check('Live theme preserves dialog drafts',await first.locator('[name="item"]').inputValue()==='Nur erster Entwurf');
    await page.click('[data-final="DS-027"]');
    await page.keyboard.press('Escape');check('Escape closes modal and restores opener',!(await page.locator('#compare-dialog').isVisible())&&await page.locator('#compare').evaluate(node=>node===document.activeElement));
    const [jsonDownload]=await Promise.all([page.waitForEvent('download'),page.click('#export-choice')]);
    const jsonPath=path.join(out,'choice.json');await jsonDownload.saveAs(jsonPath);const choice=JSON.parse(await fs.readFile(jsonPath,'utf8'));
    check('JSON export preserves explicit decision and four-design shortlist',choice.schema==='household-design-direction'&&choice.version===2&&choice.previewOnly===true&&choice.final.id==='DS-027'&&choice.final.theme==='pink-light'&&choice.shortlist.length===4);
    await page.reload();await page.locator('.design-card').last().waitFor({state:'attached'});check('Final direction persists',/DS-027/.test(await page.locator('#final-choice').textContent()));
    await page.click('#clear-final');check('Decision cleared',/Noch keine/.test(await page.locator('#final-choice').textContent()));await page.click('#clear-final');check('Decision restored',/DS-027/.test(await page.locator('#final-choice').textContent()));
    // Measure resolved browser colors for all 100 real CSS directions, including universal pink.
    for(const theme of themes){
      await page.selectOption('#theme',theme);
      const rows=await page.evaluate(()=>{
        const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d',{willReadFrequently:true});
        const rgb=color=>{ctx.clearRect(0,0,1,1);ctx.fillStyle=color;ctx.fillRect(0,0,1,1);return [...ctx.getImageData(0,0,1,1).data].slice(0,3);};
        const luminance=values=>values.map(value=>{value/=255;return value<=.04045?value/12.92:((value+.055)/1.055)**2.4;}).reduce((sum,value,index)=>sum+value*[.2126,.7152,.0722][index],0);
        const contrast=(a,b)=>{const x=luminance(rgb(a)),y=luminance(rgb(b));return(Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
        return [...document.querySelectorAll('#gallery .world')].map(node=>{
          const style=getComputedStyle(node);const token=name=>style.getPropertyValue('--world-'+name).trim();
          const pairs=[['body',token('ink'),token('paper'),4.5],['muted',token('muted'),token('paper'),4.5],['field',token('ink'),token('field'),4.5],['button',token('accent-ink'),token('accent'),4.5],['focus',token('focus'),token('paper'),3]];
          return{id:node.dataset.design,font:style.fontFamily,paper:token('paper'),accent:token('accent'),pairs:pairs.map(([name,fg,bg,min])=>({name,ratio:contrast(fg,bg),min,fg,bg}))};
        });
      });
      report.contrast.push({theme,designs:rows});check(theme+':100 computed palettes',rows.length===100);
      if(theme.startsWith('pink'))check(theme+':universal pink maintained',new Set(rows.map(row=>row.accent)).size===1&&new Set(rows.map(row=>row.paper)).size===1);
    }
    for(const width of [1440,390]){
      await page.setViewportSize({width,height:1000});
      const filtersCollapsed=!(await page.locator('#fixture').isVisible());
      if(filtersCollapsed)await page.locator('#filter-disclosure>summary').click();
      await page.selectOption('#fixture','chores');
      if(filtersCollapsed)await page.locator('#filter-disclosure>summary').click();
      const thumbnails=await page.evaluate(()=>{
        const cards=[...document.querySelectorAll('.design-card')];
        cards.forEach(node=>{node.dataset.measureStyle=node.getAttribute('style')||'';node.style.contentVisibility='visible';});
        const rows=[...document.querySelectorAll('#gallery .world')].map(node=>{
          const outer=node.getBoundingClientRect();
          const visible=[...node.querySelectorAll('.world-field')].filter(field=>getComputedStyle(field).display!=='none');
          const violations=[];
          for(const field of visible){
            for(const control of field.querySelectorAll('label,input,select,textarea')){
              const rect=control.getBoundingClientRect();
              // Single-line native inputs may internally scroll long values; only labels must wrap their full text.
              if(rect.left<outer.left-1||rect.right>outer.right+1||(control.tagName==='LABEL'&&control.scrollWidth>control.clientWidth+2))violations.push({field:field.dataset.field,tag:control.tagName,width:rect.width,scrollWidth:control.scrollWidth,clientWidth:control.clientWidth});
            }
          }
          const form=node.querySelector('form').getBoundingClientRect(),nav=node.querySelector('nav').getBoundingClientRect();
          return{id:node.dataset.design,visibleFields:visible.length,violations,form:{left:form.left,top:form.top,width:form.width},nav:{right:nav.right,bottom:nav.bottom,width:nav.width},firstInputWidth:visible[0].querySelector('input,select,textarea').getBoundingClientRect().width};
        });
        cards.forEach(node=>{node.setAttribute('style',node.dataset.measureStyle);delete node.dataset.measureStyle;});
        return rows;
      });
      report.thumbnails.push({width,designs:thumbnails});
      check(width+':all100 thumbnails expose two sample fields',thumbnails.length===100&&thumbnails.every(row=>row.visibleFields===2));
      const sidebar=thumbnails.find(row=>row.id==='DS-003');
      check(width+':DS003 sidebar does not collapse main form',sidebar.form.width>=180&&sidebar.firstInputWidth>=150&&(sidebar.form.left>=sidebar.nav.right-1||sidebar.form.top>=sidebar.nav.bottom-1));
      if(thumbnails.some(row=>row.violations.length))report.functionalIssues.push(width+': thumbnail horizontal field bounds exceeded; see thumbnails evidence.');
      for(const theme of themes){
        await page.selectOption('#theme',theme);await page.evaluate(()=>scrollTo(0,0));
        const galleryFile=`${width}-${theme}-gallery.png`;await page.screenshot({path:path.join(out,galleryFile)});report.screens.push(galleryFile);
        await page.click('#compare');await page.selectOption('#compare-fixture','chores');
        const dimensions=await page.evaluate(()=>({body:document.documentElement.scrollWidth>innerWidth+1,dialog:document.querySelector('#compare-dialog').scrollWidth>document.querySelector('#compare-dialog').clientWidth+2,components:[...document.querySelectorAll('#comparison-grid .world')].filter(node=>node.scrollWidth>node.clientWidth+2).map(node=>node.dataset.design),duplicateIds:(()=>{const ids=[...document.querySelectorAll('[id]')].map(node=>node.id);return ids.length-new Set(ids).size;})()}));
        report.overflow.push({width,theme,...dimensions});
        const results=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.a11y.push({width,theme,view:'comparison',violations:results.violations.map(v=>({id:v.id,impact:v.impact,nodes:v.nodes.map(n=>({target:n.target,summary:n.failureSummary}))}))});
        const filename=`${width}-${theme}-comparison.png`;await page.screenshot({path:path.join(out,filename)});report.screens.push(filename);
        if(theme==='pink-light'){
          const dialog=page.locator('#compare-dialog');await dialog.evaluate(node=>{node.dataset.previousStyle=node.getAttribute('style')||'';node.style.maxHeight='none';node.style.height='auto';node.style.position='absolute';node.style.inset='0 auto auto 0';});
          const fullFile=`${width}-${theme}-comparison-full.png`;await dialog.screenshot({path:path.join(out,fullFile)});report.screens.push(fullFile);await dialog.evaluate(node=>{node.setAttribute('style',node.dataset.previousStyle);delete node.dataset.previousStyle;});
        }
        await page.click('#close-compare');
      }
    }
    for(const width of [320,768,1920]){await page.setViewportSize({width,height:1000});check(width+':no horizontal page overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));}
    await page.setViewportSize({width:1440,height:1000});await page.click('#compare');await page.selectOption('#compare-fixture','smart-home');
    const [codeDownload]=await Promise.all([page.waitForEvent('download'),page.click('[data-code="DS-027"]')]);
    const codePath=path.join(out,'component-DS-027.html');await codeDownload.saveAs(codePath);const code=await fs.readFile(codePath,'utf8');
    check('Standalone HTML embeds all styles without imports',!code.includes('@import')&&code.includes('pink-dark'));
    const component=await context.newPage();component.on('pageerror',error=>report.errors.push('export: '+error.message));await component.goto(pathToFileURL(codePath).href);
    for(const theme of themes){await component.selectOption('#export-theme',theme);check('Standalone theme '+theme,await component.locator('html').getAttribute('data-theme')===theme);}
    const temperature=component.locator('[name="temperature"]');await temperature.focus();const before=await temperature.inputValue();await temperature.press('ArrowRight');check('Exported native range remains interactive',await temperature.inputValue()!==before&&await component.locator('output').textContent()===await temperature.inputValue());
    await component.locator('[type="submit"]').click();check('Exported validation and review work',await component.locator('.world-review').isVisible());await component.close();await page.click('#close-compare');
    await page.selectOption('#fixture','chores');await page.selectOption('#theme','pink-light');await page.fill('#query','DS-003');await page.waitForFunction(()=>document.querySelectorAll('.design-card').length===1);
    await page.locator('[data-card="DS-003"]').screenshot({path:path.join(out,'1440-pink-light-DS003-gallery.png')});report.screens.push('1440-pink-light-DS003-gallery.png');await page.click('#reset-filters');
    while(await page.locator('[data-remove]').count())await page.locator('[data-remove]').first().click();
    await page.click('[data-shortlist="DS-004"]');await page.click('[data-shortlist="DS-092"]');await page.click('#compare');
    const pair=page.locator('#compare-dialog');await pair.evaluate(node=>{node.dataset.previousStyle=node.getAttribute('style')||'';node.style.maxHeight='none';node.style.height='auto';node.style.position='absolute';node.style.inset='0 auto auto 0';});
    await pair.screenshot({path:path.join(out,'1440-pink-light-DS004-vs-DS092-full.png')});report.screens.push('1440-pink-light-DS004-vs-DS092-full.png');await pair.evaluate(node=>{node.setAttribute('style',node.dataset.previousStyle);delete node.dataset.previousStyle;});await page.click('#close-compare');
    await page.emulateMedia({reducedMotion:'reduce'});check('Reduced motion removes transitions',await page.locator('[data-preview]').first().evaluate(node=>getComputedStyle(node).transitionDuration)==='0s');
    await page.goto(url);await page.keyboard.press('Tab');check('Keyboard skip link receives focus',await page.locator('.skip-link').evaluate(node=>node===document.activeElement));await page.keyboard.press('Enter');check('Skip link focuses gallery',await page.locator('#gallery').evaluate(node=>node===document.activeElement));
    const noJS=await browser.newContext({javaScriptEnabled:false});const noJSPage=await noJS.newPage();await noJSPage.goto(url);check('No-JS explanation visible',await noJSPage.locator('noscript h2').isVisible());await noJSPage.goto(url+'guideline.html');check('Guideline readable without JavaScript',await noJSPage.locator('h1').isVisible());await noJS.close();
    const blocked=await browser.newContext();await blocked.addInitScript(()=>Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Blocked','SecurityError');}}));const blockedPage=await blocked.newPage();await blockedPage.goto(url);await blockedPage.locator('.design-card').last().waitFor({state:'attached'});check('Blocked storage keeps 100 designs usable',await blockedPage.locator('.design-card').count()===100&&await blockedPage.locator('#storage-warning').isVisible());await blocked.close();
    const malformed=await browser.newContext();await malformed.addInitScript(()=>{localStorage.setItem('household.design-directions.v2','{invalid');localStorage.setItem('household.design-preview.v1','unchanged sentinel');});const malformedPage=await malformed.newPage();await malformedPage.goto(url);await malformedPage.locator('.design-card').last().waitFor({state:'attached'});check('Malformed V2 storage recovers',await malformedPage.locator('.design-card').count()===100);check('V1 storage preserved',await malformedPage.evaluate(()=>localStorage.getItem('household.design-preview.v1'))==='unchanged sentinel');await malformed.close();
    const invalid=await browser.newContext();await invalid.addInitScript(()=>localStorage.setItem('household.design-directions.v2',JSON.stringify({version:2,theme:'invalid',fixture:'unknown',favorites:['DS-001','invalid','DS-001'],shortlist:['DS-001','DS-002','DS-003','DS-004','DS-005'],final:{id:'invalid',theme:'pink-dark'}})));const invalidPage=await invalid.newPage();await invalidPage.goto(url);await invalidPage.locator('.design-card').last().waitFor({state:'attached'});const cleaned=await invalidPage.evaluate(()=>JSON.parse(localStorage.getItem('household.design-directions.v2')));check('Saved state validates identity/theme and caps shortlist',cleaned.theme==='light'&&cleaned.fixture==='chores'&&cleaned.favorites.length===1&&cleaned.shortlist.length===4&&cleaned.final===null);await invalid.close();
    check('No runtime errors',report.errors.length===0);check('No external requests',report.network.length===0);
    report.contrastFailures=report.contrast.flatMap(theme=>theme.designs.flatMap(design=>design.pairs.filter(pair=>pair.ratio<pair.min).map(pair=>({theme:theme.theme,id:design.id,...pair}))));
    report.pass=report.functionalIssues.length===0&&report.a11y.every(row=>!row.violations.length)&&report.overflow.every(row=>!row.body&&!row.dialog&&!row.components.length&&!row.duplicateIds)&&report.contrastFailures.length===0;
  }finally{clearTimeout(deadline);await browser.close();await fs.writeFile(path.join(out,'report.json'),JSON.stringify(report,null,2));}
  console.log(JSON.stringify({round,checks:report.checks.length,axeScans:report.a11y.length,violations:report.a11y.reduce((n,row)=>n+row.violations.length,0),contrastFailures:report.contrastFailures?.length,overflow:report.overflow.filter(row=>row.body||row.dialog||row.components.length||row.duplicateIds),pass:report.pass,report:path.join(out,'report.json')},null,2));
  if(!report.pass)process.exitCode=1;
}
async function focusedDS060(){
  await fs.mkdir(out,{recursive:true});
  const browser=await chromium.launch({channel:'msedge',headless:true,args:['--disable-extensions']});
  const deadline=setTimeout(()=>browser.close().catch(()=>{}),90000);deadline.unref();
  try{
    const context=await browser.newContext({viewport:{width:1440,height:1000}});context.setDefaultTimeout(10000);const page=await context.newPage();
    page.on('pageerror',error=>report.errors.push(error.message));await page.goto(url);await page.locator('.design-card').last().waitFor({state:'attached'});
    await page.click('[data-shortlist="DS-004"]');await page.click('[data-shortlist="DS-060"]');await page.click('#compare');
    const design=page.locator('#comparison-grid .world[data-design="DS-060"]');
    check('DS060 latest timeline/brackets/steps loaded',await design.getAttribute('data-layout')==='timeline'&&await design.getAttribute('data-treatment')==='brackets'&&await design.getAttribute('data-navigation')==='steps');
    for(const width of [1440,390]){
      await page.setViewportSize({width,height:1000});
      for(const theme of themes){
        await page.selectOption('#compare-theme',theme);await design.locator('[name="title"]').fill('Signalarchitektur '+theme);await design.locator('[type="submit"]').click();check(`${width}/${theme}:valid native workflow`,await design.locator('.world-review').isVisible());await design.locator('[data-back]').click();
        const state=await design.evaluate(node=>{
          const style=getComputedStyle(node),canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d',{willReadFrequently:true});
          const luminance=color=>{ctx.fillStyle=color;ctx.fillRect(0,0,1,1);return [...ctx.getImageData(0,0,1,1).data].slice(0,3).map(value=>{value/=255;return value<=.04045?value/12.92:((value+.055)/1.055)**2.4;}).reduce((sum,value,index)=>sum+value*[.2126,.7152,.0722][index],0);};
          const token=name=>style.getPropertyValue('--world-'+name).trim();const pairs=[['ink','paper',4.5],['muted','paper',4.5],['ink','field',4.5],['accent-ink','accent',4.5],['focus','paper',3]].map(([fg,bg,min])=>{const a=luminance(token(fg)),b=luminance(token(bg));return{fg,bg,min,ratio:(Math.max(a,b)+.05)/(Math.min(a,b)+.05)};});
          const bounds=node.getBoundingClientRect();const overflow=[...node.querySelectorAll('label,input,select,textarea')].filter(control=>{const rect=control.getBoundingClientRect();return rect.left<bounds.left-1||rect.right>bounds.right+1;}).map(control=>control.outerHTML);
          return{pairs,overflow,bodyOverflow:document.documentElement.scrollWidth>innerWidth+1};
        });
        report.contrast.push({width,theme,...state});check(`${width}/${theme}:five contrasted token pairs`,state.pairs.every(pair=>pair.ratio>=pair.min));check(`${width}/${theme}:native controls stay in bounds`,!state.overflow.length&&!state.bodyOverflow);
        if(width===1440&&theme==='pink-light'){
          const modal=page.locator('#compare-dialog');await modal.evaluate(node=>{node.style.maxHeight='none';node.style.height='auto';node.style.position='absolute';node.style.inset='0 auto auto 0';});const filename='1440-pink-light-DS004-vs-DS060-full.png';await modal.screenshot({path:path.join(out,filename)});report.screens.push(filename);await modal.evaluate(node=>node.removeAttribute('style'));
        }
      }
    }
    check('DS060 no runtime errors',report.errors.length===0);report.pass=true;
  }finally{clearTimeout(deadline);await browser.close();await fs.writeFile(path.join(out,'report-ds060.json'),JSON.stringify(report,null,2));}
  console.log(JSON.stringify({pass:report.pass,checks:report.checks.length,report:path.join(out,'report-ds060.json')},null,2));
}
(process.argv.includes('--focus-ds060')?focusedDS060():main()).catch(async error=>{report.failure=error.stack;await fs.mkdir(out,{recursive:true});await fs.writeFile(path.join(out,'report.json'),JSON.stringify(report,null,2));console.error(error);console.error('Evidence: '+out);process.exitCode=1;});
