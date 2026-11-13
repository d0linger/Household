'use strict';

// Run within the caller's authorized synthetic browser session after rebuilding assets.
// This helper owns neither credentials nor application/container lifecycle.
async function runChoresEditorPolishChecks(page,{persist=false,preview:checkPreviews=false}={}) {
 checkPreviews=checkPreviews||persist;
 const origin=new URL(page.url());
 if(!['127.0.0.1','localhost'].includes(origin.hostname)||origin.port!=='8101')throw Error('Chores editor checks require isolated Household8101');
 const checks=[],must=(condition,name)=>{if(!condition)throw Error(name);checks.push(name);};
 const ready=async()=>{await page.waitForFunction(()=>document.querySelector('#chores-workspace')?.getAttribute('aria-busy')==='false');must(await page.locator('#chores-error').isHidden(),'Editor request completed without an unresolved error');};
 const dirty=()=>page.evaluate(()=>!window.dispatchEvent(new Event('beforeunload',{cancelable:true})));
 const previewButton=()=>page.getByRole('button',{name:'Vollständige Vorschau prüfen',exact:true});
 const applyButton=()=>page.getByRole('button',{name:'Geprüfte zukünftige Serie speichern',exact:true});
 const preview=async()=>{
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/chores/series/preview'&&r.request().method()==='POST'),previewButton().click()]);
  must(response.status()===200,'Current editor has a real accepted preview');await ready();return response.json();
 };
 await page.goto(origin.origin+'/m/chores/workspace/series/new');await ready();
 const title=page.locator('[name=title]'),kind=page.locator('[name=kind]'),anchor=page.locator('[name=anchor]'),interval=page.locator('[name=interval]');
 const weekdayTitle=page.locator('p').filter({hasText:/^Wochentage$/}),monthTitle=page.locator('p').filter({hasText:/^Saisonmonate/});
 must(!await dirty(),'Loading a new editor does not create unsaved changes');
 must(await interval.locator('..').isHidden()&&await anchor.locator('..').isHidden(),'One-time tasks hide interval and anchor labels with their controls');
 must(await weekdayTitle.isHidden()&&await monthTitle.isHidden(),'One-time tasks hide complete weekday and season groups');
 must(await page.locator('[name=assignee_id]').isHidden()&&await page.getByLabel('Feste Person suchen',{exact:true}).isHidden(),'Unassigned tasks hide the fixed-person selector and its search');
 await page.getByLabel('Bereich suchen',{exact:true}).fill('draft-search-only');
 must(!await dirty(),'Reference search text is not treated as a draft edit');await page.getByLabel('Bereich suchen',{exact:true}).fill('');
 await title.fill('Reversible draft');must(await dirty(),'An actual title edit is protected');await title.fill('');must(!await dirty(),'Reverting the title clears the unsaved-change condition');
 for(const [add,remove] of [
  ['Unteraufgabe hinzufügen','Unteraufgabe entfernen'],
  ['Person zur Eignungsliste hinzufügen','Person entfernen'],
  ['Voraussetzung hinzufügen','Voraussetzung entfernen']
 ]){
  await page.getByRole('button',{name:add,exact:true}).click();await ready();must(await dirty(),add+' is protected before typing');
  await page.getByRole('button',{name:remove,exact:true}).click();await ready();must(!await dirty(),remove+' restores the unchanged empty draft');
 }
 await kind.selectOption('days');await interval.fill('4');
 const month=String(new Date().getUTCMonth()+1);await page.locator('[name=month][value="'+month+'"]').check();
 await anchor.selectOption('completion');must(await monthTitle.isHidden(),'Completion-based schedules hide season fields');
 await kind.selectOption('weekly');must(await monthTitle.isVisible()&&await weekdayTitle.isVisible(),'Switching from completion to weekly immediately exposes weekly and season groups');
 await page.locator('[name=weekday][value="1"]').check();
 const identity=await page.evaluate(async()=>{const r=await fetch('/api/v1/core/bootstrap',{cache:'no-store'});if(!r.ok)throw Error('Fixture identity unavailable');return(await r.json()).user.membership_id;});
 await page.locator('[name=assignment_kind]').selectOption('fixed');await page.locator('[name=assignee_id]').selectOption(identity);
 await page.locator('[name=assignment_kind]').selectOption('unassigned');
 await kind.selectOption('once');
 const draftTitle='Chores editor polish '+Date.now();await title.fill(draftTitle);
 if(persist){
  const calendar=await page.evaluate(async()=>{const response=await fetch('/api/v1/chores/tasks?scope=household&limit=1',{cache:'no-store'});if(response.status!==200)throw Error('Fixture calendar unavailable: HTTP '+response.status);const body=await response.json();return {today:body.today,timezone:body.timezone};});
  await page.locator('[name=starts_on]').fill(calendar.today);await page.locator('[name=effective_on]').fill(calendar.today);
  await page.locator('[name=timezone]').fill(calendar.timezone);await page.locator('[name=local_time]').fill('00:00');
 }
 if(checkPreviews){
  const once=await preview();
  must(once.normalized.schedule.kind==='once'&&once.normalized.schedule.interval===1&&once.normalized.schedule.anchor==='scheduled'&&once.normalized.schedule.weekdays.length===0&&once.normalized.schedule.months.length===0,'Hidden recurring choices do not leak into the one-time preview command');
  must(once.normalized.assignee_id==='','Hidden fixed-person choice does not leak into unassigned preview');
  must(await dirty(),'Previewing a changed draft does not mark it saved');
 }
 const originalPath=new URL(page.url()).pathname;
 const dismissed=new Promise(resolve=>page.once('dialog',async dialog=>{const type=dialog.type();await dialog.dismiss();resolve(type);}));
 await page.getByRole('link',{name:'Serien',exact:true}).click();must(await dismissed==='beforeunload','Real navigation requests the native unsaved-change confirmation');
 must(new URL(page.url()).pathname===originalPath&&await title.inputValue()===draftTitle,'Dismissing navigation keeps the exact draft');
 await kind.selectOption('weekly');
 must(await interval.inputValue()==='4'&&await page.locator('[name=weekday][value="1"]').isChecked()&&await page.locator('[name=month][value="'+month+'"]').isChecked(),'Recurrence choices survive hiding and reopening their groups');
 if(checkPreviews){const weekly=await preview();must(weekly.normalized.schedule.anchor==='scheduled'&&weekly.normalized.schedule.months.includes(Number(month)),'Weekly preview uses a calendar anchor after leaving completion mode');}
 await kind.selectOption('days');must(await anchor.inputValue()==='completion'&&await monthTitle.isHidden(),'Returning to daily recurrence restores its completion anchor draft');
 await page.locator('[name=assignment_kind]').selectOption('fixed');must(await page.locator('[name=assignee_id]').inputValue()===identity,'Returning to fixed assignment restores the chosen person');
 await kind.selectOption('once');await page.locator('[name=assignment_kind]').selectOption('unassigned');
 if(checkPreviews){
  await preview();
  for(const [add,remove] of [
  ['Unteraufgabe hinzufügen','Unteraufgabe entfernen'],
  ['Person zur Eignungsliste hinzufügen','Person entfernen'],
  ['Voraussetzung hinzufügen','Voraussetzung entfernen']
 ]){
  await page.getByRole('button',{name:add,exact:true}).click();await ready();must(await applyButton().isHidden(),add+' invalidates the previously accepted preview');
  await page.getByRole('button',{name:remove,exact:true}).click();await ready();must(await applyButton().isHidden(),remove+' requires a fresh preview');await preview();
 }
 let releasePreview,previewFetched;
 const release=new Promise(resolve=>{releasePreview=resolve;}),fetched=new Promise(resolve=>{previewFetched=resolve;});
 const delayedPreview=async route=>{const response=await route.fetch();previewFetched();await release;await route.fulfill({response});};
 await page.route('**/api/v1/chores/series/preview',delayedPreview);
 try{
  await previewButton().click();await fetched;await title.fill(draftTitle+' latest');releasePreview();await ready();
  must(await applyButton().isHidden(),'A preview arriving after a title edit cannot be applied');
  must(await dirty(),'Edits made while previewing remain protected');
 }finally{releasePreview();await page.unroute('**/api/v1/chores/series/preview',delayedPreview);}
  await preview();
 }
 if(!persist){
  const accepted=new Promise(resolve=>page.once('dialog',async dialog=>{const type=dialog.type();await dialog.accept();resolve(type);}));
  await page.getByRole('link',{name:'Serien',exact:true}).click();must(await accepted==='beforeunload','Discarding a draft requires the native confirmation');
  await page.waitForURL(url=>url.pathname==='/m/chores/workspace/series');await ready();
  return {checks,persisted:false,notRun:[...(!checkPreviews?['Normalized preview, dynamic preview invalidation and stale response checks require preview:true']:[]),'Verified save navigation and clean saved-preview checks require explicit persist:true in the synthetic fixture']};
 }
 let saveDialogs=0;
 const rejectSaveDialog=async dialog=>{saveDialogs++;await dialog.dismiss();};page.on('dialog',rejectSaveDialog);
 try{
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/chores/series'&&r.request().method()==='POST'),applyButton().click()]);
  must(response.status()===200,'The verified series save is accepted by the real API');
  await page.waitForURL(url=>/^\/m\/chores\/workspace\/series\/[0-9a-f-]+$/.test(url.pathname));await ready();
 }finally{page.off('dialog',rejectSaveDialog);}
 must(saveDialogs===0,'Verified successful save navigates without an unsaved-change prompt');
 must(await title.inputValue()===draftTitle+' latest'&&!await dirty(),'Reloaded saved draft is clean and keeps the latest title');
 await preview();must(!await dirty(),'Previewing an unchanged saved series stays clean');
 await page.getByLabel('Bereich suchen',{exact:true}).fill('reference lookup');
 must(!await dirty()&&await applyButton().isVisible(),'Searching reference options leaves an unchanged accepted preview clean');
 const templateID=new URL(page.url()).pathname.split('/').at(-1);
 const readAPI=path=>page.evaluate(async path=>{const response=await fetch(path,{cache:'no-store'});if(response.status!==200)throw Error('Persisted fixture read failed: HTTP '+response.status+' '+path);return response.json();},path);
 const tasksPath='/api/v1/chores/tasks?'+new URLSearchParams({scope:'household',state:'all',template:templateID,limit:'2'});
 const [beforeSeries,beforeTasks]=await Promise.all([readAPI('/api/v1/chores/series/'+templateID),readAPI(tasksPath)]);
 must(beforeTasks.items.length===1&&!beforeTasks.next_cursor,'The persisted one-time fixture has exactly one historical occurrence');
 const occurrenceID=beforeTasks.items[0].occurrence.id,beforeWork=await readAPI('/api/v1/chores/occurrences/'+occurrenceID);
 must(beforeWork.item.occurrence.template_id===templateID&&beforeWork.item.detail.template_version_id===beforeSeries.snapshot.id,'The original occurrence captures the exact created series version');
 const historicalRecord=work=>JSON.stringify({occurrence:work.item.occurrence,detail:work.item.detail,snapshot:work.snapshot,checklist:work.checklist,facts:work.facts});
 const originalHistory=historicalRecord(beforeWork),editedTitle=draftTitle+' edited existing';
 await title.fill(editedTitle);must(await dirty()&&await applyButton().isHidden(),'Editing an existing series protects the draft and invalidates its old preview');
 const existingPreview=await preview();
 must(existingPreview.normalized.id===templateID&&existingPreview.normalized.base_version===beforeSeries.template.version&&existingPreview.normalized.version===beforeSeries.profile.version,'Existing-series preview carries the same identity and current version guards');
 must(existingPreview.normalized.schedule.local_time==='00:00'&&Date.parse(beforeWork.item.occurrence.due_at)<Date.parse(existingPreview.effective_at),'The regression edits a one-time slot strictly before its new version becomes effective');
 must(existingPreview.projection.slots.length===0&&existingPreview.affected_open===0,'Existing-series preview preserves the elapsed occurrence without advertising a replacement');
 let editDialogs=0;
 const rejectEditDialog=async dialog=>{editDialogs++;await dialog.dismiss();};page.on('dialog',rejectEditDialog);
 try{
  const [response]=await Promise.all([
   page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/chores/series'&&r.request().method()==='POST'),
   page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/chores/series/'+templateID&&r.request().method()==='GET'),
   applyButton().click()
  ]);
  must(response.status()===200,'The real existing-series save succeeds after its accepted preview');
  await ready();must(new URL(page.url()).pathname==='/m/chores/workspace/series/'+templateID&&await title.inputValue()===editedTitle&&!await dirty(),'Saved existing-series navigation reloads the edited title with a clean draft');
  await page.reload({waitUntil:'domcontentloaded'});await ready();
  must(editDialogs===0&&await title.inputValue()===editedTitle&&!await dirty(),'Reloading the edited existing series stays clean without an erroneous leave-page prompt');
 }finally{page.off('dialog',rejectEditDialog);}
 const [afterSeries,afterTasks,afterWork]=await Promise.all([readAPI('/api/v1/chores/series/'+templateID),readAPI(tasksPath),readAPI('/api/v1/chores/occurrences/'+occurrenceID)]);
 must(afterSeries.template.id===templateID&&afterSeries.profile.template_id===templateID&&afterSeries.template.title===editedTitle&&afterSeries.snapshot.title===editedTitle,'A fresh API read confirms the edited title on the same series');
 must(afterSeries.profile.current_version_id===afterSeries.snapshot.id&&afterSeries.snapshot.id!==beforeSeries.snapshot.id&&afterSeries.snapshot.revision>beforeSeries.snapshot.revision,'The series points to a new immutable reviewed version');
 must(afterSeries.profile.version>beforeSeries.profile.version&&afterSeries.template.version>beforeSeries.template.version,'Existing-series save advances both optimistic version guards');
 must(afterTasks.items.length===1&&!afterTasks.next_cursor&&afterTasks.items[0].occurrence.id===occurrenceID,'Existing-series edit creates no duplicate or replacement for the elapsed occurrence');
 must(historicalRecord(afterWork)===originalHistory,'Existing-series edit preserves the complete historical occurrence, captured snapshot, checklist and facts');
 await preview();must(!await dirty(),'Previewing the reloaded edited series leaves its accepted draft clean');
 await page.goto(origin.origin+'/m/chores/workspace/series/new');await ready();
 const loadedTitle=await title.inputValue(),savedTitle=draftTitle+' accepted while editing';let raceTemplateID='';
 must(loadedTitle===''&&!await dirty(),'The delayed-save fixture begins with the actual empty loaded baseline');
 await title.fill(savedTitle);await preview();
 let releaseSave,saveFetched,saveHandling=Promise.resolve(),saveRouteFailure=null,saveTestFailure=null;
 const saveRelease=new Promise(resolve=>{releaseSave=resolve;}),saveResponse=new Promise(resolve=>{saveFetched=resolve;});
 const delayedSave=route=>{
  saveHandling=(async()=>{
   try{const response=await route.fetch(),body=await response.json();saveFetched({status:response.status(),error:body.error||'',id:body.template?.id||body.profile?.template_id||body.id});await saveRelease;await route.fulfill({response});}
   catch(error){saveRouteFailure=error;saveFetched({status:0,error:error.message});}
  })();return saveHandling;
 };
 await page.route('**/api/v1/chores/series',delayedSave);
 try{
  await applyButton().click();const outcome=await saveResponse;must(outcome.status===200,'Delayed real save is accepted before its response reaches the editor (HTTP '+outcome.status+(outcome.error?': '+outcome.error:'')+')');
  raceTemplateID=outcome.id;must(/^[0-9a-f-]{36}$/i.test(raceTemplateID||''),'The accepted delayed create identifies its real synthetic series');await title.fill(loadedTitle);
  must(!await dirty(),'Restoring the loaded baseline is clean before the held save response arrives');
  const refusedNavigation=page.waitForEvent('dialog').then(async dialog=>{const type=dialog.type();await dialog.dismiss();return type;});
  releaseSave();must(await refusedNavigation==='beforeunload','Restoring the original title during an accepted save still requests leave-page confirmation');await ready();
  must(await title.inputValue()===loadedTitle&&await dirty(),'Dismissing post-save navigation preserves the later draft and compares it with the accepted save');
  const persisted=await page.evaluate(async id=>{const response=await fetch('/api/v1/chores/series/'+id,{cache:'no-store'});if(!response.ok)throw Error('Saved fixture unavailable');return(await response.json()).snapshot.title;},raceTemplateID);
  must(persisted===savedTitle,'The server holds the submitted title while the restored local title remains unsaved');
 }catch(error){saveTestFailure=error;}finally{
  releaseSave();await saveHandling;await page.unroute('**/api/v1/chores/series',delayedSave);
  if(saveRouteFailure){if(saveTestFailure)saveTestFailure.message+='; interception cleanup: '+saveRouteFailure.message;else saveTestFailure=saveRouteFailure;}
 }
 if(saveTestFailure)throw saveTestFailure;
 const discardLaterDraft=page.waitForEvent('dialog').then(async dialog=>{const type=dialog.type();await dialog.accept();return type;});
 await page.getByRole('link',{name:'Serien',exact:true}).click();must(await discardLaterDraft==='beforeunload','Leaving the later draft still requires explicit confirmation');
 await page.waitForURL(url=>url.pathname==='/m/chores/workspace/series');await ready();
 return {checks,persisted:true,templateID,occurrenceID,raceTemplateID};
}

module.exports={runChoresEditorPolishChecks};
