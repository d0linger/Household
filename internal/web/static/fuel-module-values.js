const HouseholdFuelModuleValues=(()=>{
 'use strict';
 const UUID=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
 const fail=message=>{throw Error(message);};
 function decimal(raw,{positive=false,nullable=false,scale=6}={}){if((raw===null||raw===undefined)&&nullable)return null;if(typeof raw!=='string')fail('Dezimalwert muss als exakte Zeichenfolge vorliegen.');const text=raw.trim().replace(',','.');if(!text&&nullable)return null;if(!new RegExp('^\\d{1,12}(?:\\.\\d{1,'+scale+'})?$').test(text))fail('Dezimalwert mit höchstens '+scale+' Nachkommastellen eingeben.');const[whole,fraction='']=text.split('.'),integer=BigInt(whole+fraction.padEnd(scale,'0'));if(positive&&integer===0n)fail('Wert muss größer als null sein.');return BigInt(whole).toString()+(fraction?'.'+fraction:'');}
 function majorToMinor(raw,{nullable=false}={}){const text=decimal(raw,{nullable,scale:2});if(text===null)return null;const[whole,fraction='']=text.split('.');return(BigInt(whole)*100n+BigInt(fraction.padEnd(2,'0'))).toString();}
 function minorToMajor(raw){if(raw===null||raw===undefined||raw==='')return null;if(typeof raw!=='string')return null;const text=raw;if(!/^-?\d{1,64}$/.test(text))return null;const n=BigInt(text),abs=n<0n?-n:n;return(n<0n?'−':'')+(abs/100n).toString()+','+(abs%100n).toString().padStart(2,'0');}
 function exact(raw){if(raw===null||raw===undefined||raw===''||typeof raw!=='string')return'Nicht bekannt';return raw.replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');}
 function civil(raw,{nullable=false}={}){const s=String(raw??'');if(!s&&nullable)return null;if(!/^\d{4}-\d{2}-\d{2}$/.test(s)||s<'1900-01-01'||s>'2199-12-31')fail('Kalenderdatum erneut prüfen.');const d=new Date(s+'T12:00:00Z');if(!Number.isFinite(d.valueOf())||d.toISOString().slice(0,10)!==s)fail('Kalenderdatum ist ungültig.');return s;}
 function validateUUID(raw,{nullable=false}={}){const s=String(raw??'');if(!s&&nullable)return null;if(!UUID.test(s))fail('Aktuellen berechtigten Eintrag auswählen.');return s.toLowerCase();}
 function integer(raw,min,max,{nullable=false}={}){const s=String(raw??'').trim();if(!s&&nullable)return null;if(!/^\d+$/.test(s)||BigInt(s)<BigInt(min)||BigInt(s)>BigInt(max))fail('Ganze Zahl zwischen '+min+' und '+max+' eingeben.');return s;}
 function text(raw,max,{nullable=false}={}){const s=String(raw??'').trim();if(!s&&nullable)return null;if([...s].length>max||s.includes('\0'))fail('Text auf '+max+' Zeichen begrenzen.');return s;}
 function unit(fuel){return({petrol:'l',diesel:'l',lpg:'l',cng:'kg',electric:'kWh',hydrogen:'kg'})[fuel]||null;}
 function canonical(value){if(Array.isArray(value))return value.map(canonical);if(value&&typeof value==='object')return Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])]));return value;}
 function commandBytes(command){return new TextEncoder().encode(JSON.stringify(canonical(command))).length;}
 function chartPoints(rows,key){const source=rows.map(r=>r.below_precision?'':typeof r[key]==='string'?r[key]:''),values=source.map(s=>/^\d{1,48}(?:\.\d{1,6})?$/.test(s)?BigInt(s.split('.')[0])*1000000n+BigInt((s.split('.')[1]||'').padEnd(6,'0')):null),present=values.filter(v=>v!==null);if(!present.length)return[];const max=present.reduce((a,b)=>a>b?a:b,0n)||1n;return values.map((value,i)=>value===null?null:{x:rows.length<2?500:Math.round(i*1000/(rows.length-1)),y:1000-Number(value*1000n/max),value:source[i],row:rows[i]});}
 return{decimal,majorToMinor,minorToMajor,exact,civil,validateUUID,integer,text,unit,canonical,commandBytes,chartPoints};
})();
if(typeof module!=='undefined')module.exports=HouseholdFuelModuleValues;
