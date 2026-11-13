const _householdText_pantry_suggestions_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
(() => {
 'use strict';
 if(!document.querySelector('#pantry-suggestions'))return;
 const root=document.querySelector('#planning-suggestions');
 const suggestions=window.HouseholdRecipeSuggestions.create(root,{
  endpoint:'/api/v1/pantry/recipe-suggestions',selectable:true,selectLabel:_householdText_pantry_suggestions_js("Physischen Vorrat vergleichen"),
  onSelect(candidate,page){location.assign('/m/recipes/recipes/'+candidate.recipe_id+'/pantry?'+new URLSearchParams({version:candidate.source_version_id,servings:page.servings}));}
 });
 suggestions.reset();root.open=true;
})();
