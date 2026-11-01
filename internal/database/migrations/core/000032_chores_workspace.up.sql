BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE baseline jsonb;item record;actual text;actual_relations jsonb;actual_policies jsonb;actual_triggers jsonb;
BEGIN
 IF NOT chores.expansion_ready() OR NOT core.finance_ready()
  OR NOT mealplan.expansion_ready() OR (SELECT count(*) FROM core.audit_policies)<>121 THEN
  RAISE EXCEPTION 'Chores audit requires its exact complete predecessor';
 END IF;
 baseline=chores.predecessor_catalog();
 SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) INTO actual_policies FROM core.audit_policies policy;
 SELECT jsonb_agg(jsonb_build_array(n.nspname,t.relname,t.relkind,t.relowner::regrole::text) ORDER BY n.nspname,t.relname)
 INTO actual_relations FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE t.relkind IN('r','p')
 AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
 AND NOT(n.nspname='chores' AND t.relname IN('areas','series_profiles','template_versions','version_checklist_items','version_dependencies',
 'rotation_members','absences','occurrence_details','occurrence_checklist_items','completions','member_preferences','calendar_feeds','reminder_deliveries','schedule_state'));
 SELECT jsonb_agg(jsonb_build_array(policy.schema_name,policy.table_name,tr.tgname,pg_get_triggerdef(tr.oid),tr.tgenabled)
 ORDER BY policy.schema_name,policy.table_name,tr.tgname) INTO actual_triggers FROM core.audit_policies policy
 JOIN pg_trigger tr ON tr.tgrelid=to_regclass(format('%I.%I',policy.schema_name,policy.table_name))
 AND tr.tgname IN('household_audit_change','household_audit_truncate');
 IF actual_policies IS DISTINCT FROM baseline->'audit_policies' OR actual_relations IS DISTINCT FROM baseline->'relations'
 OR actual_triggers IS DISTINCT FROM baseline->'audit_triggers' THEN RAISE EXCEPTION 'Chores extension changed its reviewed audit/table predecessor';END IF;
 IF core.finance_catalog() IS DISTINCT FROM baseline->'finance_catalog' THEN RAISE EXCEPTION 'unreviewed Finance predecessor';END IF;
 FOR item IN SELECT key,value FROM jsonb_each(baseline->'functions') LOOP
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item.key)
   AND p.proowner::regrole::text=item.value->>'owner' AND p.proacl::text IS NOT DISTINCT FROM item.value->>'acl';
  IF actual IS DISTINCT FROM item.value->>'definition' THEN RAISE EXCEPTION 'unreviewed Chores compatibility predecessor: %',item.key;END IF;
 END LOOP;
END $predecessor$;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('chores','areas',ARRAY['id'],ARRAY['name','position','archived_at','version'],'name'),
 ('chores','series_profiles',ARRAY['template_id'],ARRAY['current_version_id','managed','paused','paused_until','paused_at','resume_at','resume_anchor_at','materialize_from','review_required','version'],NULL),
 ('chores','template_versions',ARRAY['id'],ARRAY['template_id','revision','title','area_id','estimated_minutes','difficulty','priority','rule_kind','interval_value','anchor_kind','starts_on','local_time','timezone','month_day','ends_on','effective_at','assignment_kind','assignee_id','legacy_capture','dst_policy'],'title'),
 ('chores','version_checklist_items',ARRAY['id'],ARRAY['version_id','item_key','position','required'],NULL),
 ('chores','version_dependencies',ARRAY['id'],ARRAY['version_id','template_id','prerequisite_id'],NULL),
 ('chores','rotation_members',ARRAY['id'],ARRAY['version_id','membership_id','position','eligible'],NULL),
 ('chores','absences',ARRAY['id'],ARRAY['membership_id','starts_on','ends_on','substitute_id','version'],NULL),
 ('chores','occurrence_details',ARRAY['occurrence_id'],ARRAY['template_id','template_version_id','logical_key','original_due_at','scheduled_on','timezone','anchor_kind','managed','lifecycle_state','claimed_by','claim_expires_at','skipped_reason','skipped_at','skipped_by','skip_anchor_at','version'],NULL),
 ('chores','occurrence_checklist_items',ARRAY['id'],ARRAY['occurrence_id','item_key','required','done','done_at','done_by','version'],NULL),
 ('chores','completions',ARRAY['id'],ARRAY['occurrence_id','template_id','template_version_id','completed_at','actor_user_id','actor_membership_id','actual_minutes','legacy_capture'],NULL),
 ('chores','member_preferences',ARRAY['household_id','user_id'],ARRAY['daily_minutes','max_difficulty','notification_mode','digest_enabled','digest_minute','quiet_start','quiet_end','timezone','escalation_enabled','escalation_after_days','escalation_recipient_id','allow_escalations','version'],NULL),
 ('chores','calendar_feeds',ARRAY['id'],ARRAY['user_id','issuer_membership_id','issuer_role','auth_version','module_generation','scope','days_back','days_ahead','expires_at','revoked_at','version'],NULL),
 ('chores','reminder_deliveries',ARRAY['id'],ARRAY['module_generation','recipient_user_id','local_day','stage','source_key','notification_id','delivered_at'],NULL),
 ('chores','schedule_state',ARRAY['template_id'],ARRAY['template_version_id','module_generation','generated_through','next_check_at','last_run_at','backlog','version'],NULL);

DO $compatibility$
DECLARE baseline jsonb;old_catalog jsonb;new_catalog jsonb;old_others jsonb;new_others jsonb;
 producer text;ready text;audit_definition text;needle text;replacement text;
BEGIN
 baseline=chores.predecessor_catalog();old_catalog=baseline->'finance_catalog';
 producer=baseline->'functions'->'core.finance_producers_ready()'->>'definition';
 needle='(SELECT count(*) FROM core.audit_policies)=121';
 IF (length(producer)-length(replace(producer,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed Finance audit count';END IF;
 EXECUTE replace(producer,needle,'(SELECT count(*) FROM core.audit_policies)=135');
 new_catalog=core.finance_catalog();
 SELECT jsonb_agg(value ORDER BY ordinal) INTO old_others FROM jsonb_array_elements(old_catalog->'functions') WITH ORDINALITY x(value,ordinal)
  WHERE value->>0<>'core.finance_producers_ready()';
 SELECT jsonb_agg(value ORDER BY ordinal) INTO new_others FROM jsonb_array_elements(new_catalog->'functions') WITH ORDINALITY x(value,ordinal)
  WHERE value->>0<>'core.finance_producers_ready()';
 IF old_catalog-'functions' IS DISTINCT FROM new_catalog-'functions' OR old_others IS DISTINCT FROM new_others THEN
  RAISE EXCEPTION 'Finance compatibility differs outside its one reviewed producer count';
 END IF;
 ready=baseline->'functions'->'core.finance_ready()'->>'definition';
 needle=quote_literal(old_catalog::text)||'::jsonb';replacement=quote_literal(new_catalog::text)||'::jsonb';
 IF (length(ready)-length(replace(ready,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed complete Finance readiness';END IF;
 EXECUTE replace(ready,needle,replacement);
 IF NOT core.finance_ready() THEN RAISE EXCEPTION 'Finance readiness lost during Chores extension';END IF;
 audit_definition=baseline->'functions'->'core.audit_ready()'->>'definition';
 needle='core.finance_ready() AND count(*)=121';
 IF (length(audit_definition)-length(replace(audit_definition,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed complete audit predecessor';END IF;
 EXECUTE replace(audit_definition,needle,'core.finance_ready() AND chores.expansion_ready() AND count(*)=135');
END $compatibility$;
SELECT core.install_audit_triggers();
DO $ready$
BEGIN
 IF NOT chores.expansion_ready() OR NOT core.finance_ready() OR NOT core.audit_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>135 THEN RAISE EXCEPTION 'incomplete Chores audit/Finance composition';END IF;
END $ready$;
COMMIT;
