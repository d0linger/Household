BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TABLE expenses.accounts(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),account_type text NOT NULL CHECK(account_type IN('cash','bank','card','other')),currency_code text NOT NULL,currency_exponent smallint NOT NULL,notes text NOT NULL DEFAULT '' CHECK(length(notes)<=1000),archived_at timestamptz,UNIQUE(household_id,id));
CREATE TABLE expenses.entry_finance(id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),entry_id uuid NOT NULL,original_state text NOT NULL CHECK(original_state IN('known','legacy_unknown')),original_currency text,original_exponent smallint,original_minor bigint CHECK(original_minor<>0 AND original_minor BETWEEN -1000000000000 AND 1000000000000),fx_numerator numeric(36,0),fx_denominator numeric(36,0),fx_effective_on date,fx_source text,fx_rounding text NOT NULL DEFAULT 'half_away_from_zero_v1' CHECK(fx_rounding='half_away_from_zero_v1'),fx_reviewed_by uuid REFERENCES core.users(id),fx_reviewed_at timestamptz,account_id uuid,payment_currency text,payment_exponent smallint,payment_minor bigint,payment_fx_numerator numeric(36,0),payment_fx_denominator numeric(36,0),payment_fx_on date,payment_fx_source text,payment_base_minor bigint,payment_difference_reviewed boolean NOT NULL DEFAULT false,UNIQUE(household_id,id),UNIQUE(household_id,entry_id),CHECK(id=entry_id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id) ON DELETE CASCADE,FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),CHECK((original_state='known' AND original_currency IS NOT NULL AND original_exponent IS NOT NULL AND original_minor IS NOT NULL) OR (original_state='legacy_unknown' AND original_currency IS NULL AND original_exponent IS NULL AND original_minor IS NULL)));
CREATE TABLE expenses.entry_original_lines(id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),line_id uuid NOT NULL,entry_id uuid NOT NULL,original_minor bigint NOT NULL CHECK(original_minor<>0 AND original_minor BETWEEN -1000000000000 AND 1000000000000),allocation_rule text NOT NULL DEFAULT 'signed_largest_remainder_floor_v1' CHECK(allocation_rule='signed_largest_remainder_floor_v1'),UNIQUE(household_id,id),CHECK(id=line_id),FOREIGN KEY(household_id,line_id) REFERENCES expenses.entry_lines(household_id,id) ON DELETE CASCADE,FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id) ON DELETE CASCADE);
CREATE TABLE expenses.cash_events(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),event_kind text NOT NULL CHECK(event_kind IN('expense','refund','reversal','income','transfer','opening','adjustment','settlement')),state text NOT NULL DEFAULT 'draft' CHECK(state IN('draft','posted')),occurred_on date NOT NULL,description text NOT NULL CHECK(length(description) BETWEEN 1 AND 300),entry_id uuid,original_currency text NOT NULL,original_exponent smallint NOT NULL,original_minor bigint NOT NULL CHECK(original_minor BETWEEN -1000000000000 AND 1000000000000),base_minor bigint CHECK(base_minor BETWEEN -1000000000000 AND 1000000000000),fx_numerator numeric(36,0),fx_denominator numeric(36,0),fx_effective_on date,fx_source text,reverses_event_id uuid,posted_at timestamptz,UNIQUE(household_id,id),UNIQUE(household_id,entry_id),UNIQUE(household_id,reverses_event_id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id),FOREIGN KEY(household_id,reverses_event_id) REFERENCES expenses.cash_events(household_id,id),CHECK((state='posted')=(posted_at IS NOT NULL)),CHECK(original_minor<>0 OR event_kind='opening'));
CREATE TABLE expenses.account_movements(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),event_id uuid NOT NULL,account_id uuid NOT NULL,leg smallint NOT NULL CHECK(leg BETWEEN 0 AND 2),currency_code text NOT NULL,currency_exponent smallint NOT NULL,amount_minor bigint NOT NULL CHECK(amount_minor BETWEEN -1000000000000 AND 1000000000000),base_minor bigint NOT NULL CHECK(base_minor BETWEEN -1000000000000 AND 1000000000000),fx_numerator numeric(36,0),fx_denominator numeric(36,0),fx_effective_on date,fx_source text,UNIQUE(household_id,id),UNIQUE(household_id,event_id,leg),FOREIGN KEY(household_id,event_id) REFERENCES expenses.cash_events(household_id,id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id));
CREATE TABLE expenses.entry_relations(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),entry_id uuid NOT NULL,original_entry_id uuid NOT NULL,relation text NOT NULL CHECK(relation IN('refund_of','correction_of','reversal_of')),reviewed_original_version bigint NOT NULL CHECK(reviewed_original_version>0),reversal_entry_id uuid,UNIQUE(household_id,id),UNIQUE(household_id,entry_id,relation),CHECK(entry_id<>original_entry_id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id),FOREIGN KEY(household_id,original_entry_id) REFERENCES expenses.entries(household_id,id),FOREIGN KEY(household_id,reversal_entry_id) REFERENCES expenses.entries(household_id,id));
CREATE TABLE expenses.entry_split_sets(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),entry_id uuid NOT NULL,method text NOT NULL CHECK(method IN('equal','percent','weights','fixed')),currency_code text NOT NULL,currency_exponent smallint NOT NULL,total_minor bigint NOT NULL CHECK(total_minor<>0 AND total_minor BETWEEN -1000000000000 AND 1000000000000),payer_membership_id uuid,basis jsonb NOT NULL CHECK(jsonb_typeof(basis)='array' AND jsonb_array_length(basis) BETWEEN 1 AND 50 AND octet_length(basis::text)<=16384),algorithm text NOT NULL DEFAULT 'largest_remainder_uuid_v1' CHECK(algorithm='largest_remainder_uuid_v1'),reviewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),reviewed_by uuid NOT NULL REFERENCES core.users(id),UNIQUE(household_id,id),UNIQUE(household_id,entry_id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id) ON DELETE CASCADE,FOREIGN KEY(household_id,payer_membership_id) REFERENCES core.memberships(household_id,id));
CREATE TABLE expenses.entry_person_shares(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),split_set_id uuid NOT NULL,entry_id uuid NOT NULL,membership_id uuid NOT NULL,share_minor bigint NOT NULL CHECK(share_minor BETWEEN -1000000000000 AND 1000000000000),base_minor bigint,position smallint NOT NULL CHECK(position BETWEEN 0 AND 49),UNIQUE(household_id,id),UNIQUE(household_id,split_set_id,membership_id),FOREIGN KEY(household_id,split_set_id) REFERENCES expenses.entry_split_sets(household_id,id) ON DELETE CASCADE,FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id) ON DELETE CASCADE,FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id));
CREATE TABLE expenses.settlement_details(id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),event_id uuid NOT NULL,from_membership_id uuid NOT NULL,to_membership_id uuid NOT NULL,currency_code text NOT NULL,currency_exponent smallint NOT NULL,amount_minor bigint NOT NULL CHECK(amount_minor BETWEEN 1 AND 1000000000000),reviewed_sources jsonb NOT NULL CHECK(jsonb_typeof(reviewed_sources)='array' AND jsonb_array_length(reviewed_sources)<=1000 AND octet_length(reviewed_sources::text)<=131072),reviewed_balance_hash bytea NOT NULL CHECK(octet_length(reviewed_balance_hash)=32),UNIQUE(household_id,id),CHECK(id=event_id AND from_membership_id<>to_membership_id),FOREIGN KEY(household_id,event_id) REFERENCES expenses.cash_events(household_id,id),FOREIGN KEY(household_id,from_membership_id) REFERENCES core.memberships(household_id,id),FOREIGN KEY(household_id,to_membership_id) REFERENCES core.memberships(household_id,id));
CREATE TABLE expenses.saved_filters(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),owner_user_id uuid NOT NULL,name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),criteria jsonb NOT NULL CHECK(jsonb_typeof(criteria)='object' AND octet_length(criteria::text)<=16384),criteria_version smallint NOT NULL DEFAULT 1 CHECK(criteria_version=1),UNIQUE(household_id,id),UNIQUE(household_id,owner_user_id,name),FOREIGN KEY(household_id,owner_user_id) REFERENCES core.memberships(household_id,user_id));
CREATE TABLE expenses.category_budgets(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),category_id uuid NOT NULL,starts_on date NOT NULL,ends_on date,monthly_minor bigint NOT NULL CHECK(monthly_minor BETWEEN 0 AND 1000000000000),carry_rule text NOT NULL CHECK(carry_rule IN('none','positive_only','all')),initial_carry_minor bigint NOT NULL DEFAULT 0 CHECK(initial_carry_minor BETWEEN -1000000000000 AND 1000000000000),scope jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(scope)='object' AND octet_length(scope::text)<=16384),archived_at timestamptz,UNIQUE(household_id,id),FOREIGN KEY(household_id,category_id) REFERENCES expenses.categories(household_id,id),CHECK(extract(day FROM starts_on)=1 AND (ends_on IS NULL OR (ends_on>=starts_on AND extract(day FROM ends_on)=1))));
CREATE TABLE expenses.savings_goals(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),currency_code text NOT NULL,currency_exponent smallint NOT NULL,target_minor bigint NOT NULL CHECK(target_minor BETWEEN 1 AND 1000000000000),target_on date,account_id uuid,planned_period_minor bigint CHECK(planned_period_minor BETWEEN 0 AND 1000000000000),planned_period text CHECK(planned_period IN('week','month','year')),state text NOT NULL DEFAULT 'active' CHECK(state IN('active','completed','archived')),UNIQUE(household_id,id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),CHECK((planned_period_minor IS NULL)=(planned_period IS NULL)));
CREATE TABLE expenses.goal_contributions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),goal_id uuid NOT NULL,occurred_on date NOT NULL,amount_minor bigint NOT NULL CHECK(amount_minor BETWEEN 1 AND 1000000000000),contribution_kind text NOT NULL CHECK(contribution_kind IN('planned','manual_recorded','ledger_recorded','reversal')),event_id uuid,movement_id uuid,reverses_contribution_id uuid,note text NOT NULL DEFAULT '' CHECK(length(note)<=1000),UNIQUE(household_id,id),UNIQUE(household_id,reverses_contribution_id),FOREIGN KEY(household_id,goal_id) REFERENCES expenses.savings_goals(household_id,id),FOREIGN KEY(household_id,event_id) REFERENCES expenses.cash_events(household_id,id),FOREIGN KEY(household_id,movement_id) REFERENCES expenses.account_movements(household_id,id),FOREIGN KEY(household_id,reverses_contribution_id) REFERENCES expenses.goal_contributions(household_id,id));
CREATE TABLE expenses.recurring_templates(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),anchor_on date NOT NULL,ends_on date,repeat_unit text NOT NULL CHECK(repeat_unit IN('day','week','month','year')),repeat_interval smallint NOT NULL CHECK(repeat_interval BETWEEN 1 AND 366),monthday_policy text NOT NULL CHECK(monthday_policy IN('clamp','skip')),template jsonb NOT NULL CHECK(jsonb_typeof(template)='object' AND octet_length(template::text)<=65536),account_id uuid,active boolean NOT NULL DEFAULT true,UNIQUE(household_id,id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),CHECK(ends_on IS NULL OR ends_on>=anchor_on));
CREATE TABLE expenses.recurring_occurrences(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),template_id uuid NOT NULL,scheduled_on date NOT NULL,template_version bigint NOT NULL CHECK(template_version>0),template_hash bytea NOT NULL CHECK(octet_length(template_hash)=32),entry_id uuid,event_id uuid,state text NOT NULL CHECK(state IN('proposed','draft_created','skipped','cancelled')),UNIQUE(household_id,id),UNIQUE(household_id,template_id,scheduled_on),FOREIGN KEY(household_id,template_id) REFERENCES expenses.recurring_templates(household_id,id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id),FOREIGN KEY(household_id,event_id) REFERENCES expenses.cash_events(household_id,id));
CREATE TABLE expenses.categorization_rules(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),priority integer NOT NULL CHECK(priority BETWEEN -1000000 AND 1000000),active boolean NOT NULL DEFAULT true,match_kind text NOT NULL CHECK(match_kind IN('equals','prefix','contains')),payee_pattern text NOT NULL CHECK(length(payee_pattern) BETWEEN 1 AND 200),currency_code text,minimum_minor bigint,maximum_minor bigint,category_id uuid NOT NULL,account_id uuid,UNIQUE(household_id,id),FOREIGN KEY(household_id,category_id) REFERENCES expenses.categories(household_id,id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),CHECK(minimum_minor IS NULL OR maximum_minor IS NULL OR minimum_minor<=maximum_minor));
CREATE TABLE expenses.bank_profiles(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),owner_user_id uuid NOT NULL,name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),account_id uuid NOT NULL,delimiter text NOT NULL CHECK(delimiter IN(',', ';', E'\t')),encoding text NOT NULL CHECK(encoding='utf-8'),date_layout text NOT NULL CHECK(date_layout IN('iso','dmy','slash_dmy','slash_mdy')),decimal_separator text NOT NULL CHECK(decimal_separator IN('.',',')),group_separator text NOT NULL DEFAULT '' CHECK(group_separator IN('', '.', ',', ' ')),amount_mode text NOT NULL CHECK(amount_mode IN('signed','debit_credit')),field_map jsonb NOT NULL CHECK(jsonb_typeof(field_map)='object' AND octet_length(field_map::text)<=16384),profile_format smallint NOT NULL DEFAULT 1 CHECK(profile_format=1),archived_at timestamptz,UNIQUE(household_id,id),UNIQUE(household_id,owner_user_id,name),FOREIGN KEY(household_id,owner_user_id) REFERENCES core.memberships(household_id,user_id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),CHECK(group_separator<>decimal_separator));
CREATE TABLE expenses.reconciliations(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),account_id uuid NOT NULL,starts_on date CHECK(starts_on BETWEEN '1900-01-01'::date AND '2200-12-31'::date),opening_minor bigint CHECK(opening_minor BETWEEN -1000000000000 AND 1000000000000),statement_on date NOT NULL,CHECK((starts_on IS NULL AND opening_minor IS NULL) OR(starts_on IS NOT NULL AND opening_minor IS NOT NULL AND starts_on<=statement_on)),statement_minor bigint NOT NULL CHECK(statement_minor BETWEEN -1000000000000 AND 1000000000000),currency_code text NOT NULL,currency_exponent smallint NOT NULL,computed_minor numeric(36,0) NOT NULL,difference_minor numeric(36,0) NOT NULL,scope_hash bytea NOT NULL CHECK(octet_length(scope_hash)=32),account_version bigint NOT NULL CHECK(account_version>0),posting_count integer NOT NULL CHECK(posting_count BETWEEN 0 AND 10000),state text NOT NULL CHECK(state IN('review','confirmed','reopened')),confirmed_by uuid REFERENCES core.users(id),confirmed_at timestamptz,reopens_id uuid,reopen_reason text NOT NULL DEFAULT '' CHECK(length(reopen_reason)<=1000),UNIQUE(household_id,id),FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),FOREIGN KEY(household_id,reopens_id) REFERENCES expenses.reconciliations(household_id,id));
CREATE TABLE expenses.reconciliation_items(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),reconciliation_id uuid NOT NULL,movement_id uuid NOT NULL,reviewed_event_version bigint NOT NULL CHECK(reviewed_event_version>0),reviewed_movement_version bigint NOT NULL CHECK(reviewed_movement_version>0),amount_minor bigint NOT NULL,UNIQUE(household_id,id),UNIQUE(household_id,reconciliation_id,movement_id),FOREIGN KEY(household_id,reconciliation_id) REFERENCES expenses.reconciliations(household_id,id),FOREIGN KEY(household_id,movement_id) REFERENCES expenses.account_movements(household_id,id));
CREATE TABLE expenses.receipt_reviews(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),entry_id uuid,capture_job_id uuid,attachment_id uuid,source_version bigint,source_hash bytea CHECK(source_hash IS NULL OR octet_length(source_hash)=32),currency_code text NOT NULL,currency_exponent smallint NOT NULL,receipt_total_minor bigint,line_total_minor numeric(36,0),difference_minor numeric(36,0),difference_acknowledged boolean NOT NULL DEFAULT false,state text NOT NULL CHECK(state IN('draft','confirmed','superseded')),confirmed_by uuid REFERENCES core.users(id),confirmed_at timestamptz,UNIQUE(household_id,id),FOREIGN KEY(household_id,entry_id) REFERENCES expenses.entries(household_id,id));
CREATE TABLE expenses.receipt_review_lines(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),review_id uuid NOT NULL,position smallint NOT NULL CHECK(position BETWEEN 0 AND 99),label text NOT NULL CHECK(length(label)<=300),quantity text,unit_price text,amount_minor bigint CHECK(amount_minor BETWEEN -1000000000000 AND 1000000000000),source_text text NOT NULL DEFAULT '' CHECK(length(source_text)<=2000),source_kind text NOT NULL CHECK(source_kind IN('ocr','manual')),page_number smallint CHECK(page_number BETWEEN 1 AND 20),source_line integer CHECK(source_line>0),confidence numeric CHECK(confidence BETWEEN 0 AND 100),entry_line_id uuid,reviewed boolean NOT NULL DEFAULT false,UNIQUE(household_id,id),UNIQUE(household_id,review_id,position),FOREIGN KEY(household_id,review_id) REFERENCES expenses.receipt_reviews(household_id,id),FOREIGN KEY(household_id,entry_line_id) REFERENCES expenses.entry_lines(household_id,id));

DO $metadata$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations','entry_split_sets','entry_person_shares','settlement_details','saved_filters','category_budgets','savings_goals','goal_contributions','recurring_templates','recurring_occurrences','categorization_rules','bank_profiles','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines'] LOOP
  EXECUTE format('ALTER TABLE expenses.%I ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK(version>0),ADD COLUMN created_by uuid NOT NULL REFERENCES core.users(id),ADD COLUMN changed_by uuid NOT NULL REFERENCES core.users(id),ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp(),ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp()',relation);
 END LOOP;
END $metadata$;

CREATE INDEX finance_accounts_active ON expenses.accounts(household_id,archived_at,id);
CREATE INDEX finance_entry_account ON expenses.entry_finance(household_id,account_id,entry_id);
CREATE INDEX finance_original_parent ON expenses.entry_original_lines(household_id,entry_id,id);
CREATE INDEX finance_event_dates ON expenses.cash_events(household_id,occurred_on DESC,id);
CREATE INDEX finance_account_statement ON expenses.account_movements(household_id,account_id,event_id,id);
CREATE INDEX finance_original_relations ON expenses.entry_relations(household_id,original_entry_id,relation,id);
CREATE INDEX finance_member_shares ON expenses.entry_person_shares(household_id,membership_id,entry_id);
CREATE INDEX finance_filter_owner ON expenses.saved_filters(household_id,owner_user_id,id);
CREATE INDEX finance_budget_dates ON expenses.category_budgets(household_id,category_id,starts_on,id);
CREATE INDEX finance_goal_state ON expenses.savings_goals(household_id,state,target_on,id);
CREATE INDEX finance_goal_progress ON expenses.goal_contributions(household_id,goal_id,occurred_on,id);
CREATE INDEX finance_template_due ON expenses.recurring_templates(household_id,active,anchor_on,id);
CREATE INDEX finance_occurrence_due ON expenses.recurring_occurrences(household_id,scheduled_on,state,id);
CREATE INDEX finance_rule_order ON expenses.categorization_rules(household_id,active,priority,id);
CREATE INDEX finance_profile_owner ON expenses.bank_profiles(household_id,owner_user_id,account_id,id);
CREATE INDEX finance_statement_dates ON expenses.reconciliations(household_id,account_id,statement_on DESC,id);
CREATE INDEX finance_statement_items ON expenses.reconciliation_items(household_id,reconciliation_id,movement_id);
CREATE INDEX finance_receipt_entry ON expenses.receipt_reviews(household_id,entry_id,id);
CREATE INDEX finance_receipt_attachment ON expenses.receipt_reviews(household_id,attachment_id,id);
CREATE INDEX finance_receipt_lines ON expenses.receipt_review_lines(household_id,review_id,position,id);

ALTER TABLE expenses.cash_events ADD COLUMN account_id uuid,ADD COLUMN payment_context jsonb,
 ADD FOREIGN KEY(household_id,account_id) REFERENCES expenses.accounts(household_id,id),
 ADD CHECK(payment_context IS NULL OR jsonb_typeof(payment_context)='object' AND octet_length(payment_context::text)<=16384);
ALTER TABLE expenses.cash_events ADD COLUMN payee text NOT NULL DEFAULT '' CHECK(length(payee)<=200);
ALTER TABLE expenses.entry_split_sets ADD COLUMN active boolean NOT NULL DEFAULT true;
ALTER TABLE expenses.recurring_templates ADD COLUMN template_kind text NOT NULL DEFAULT 'expense' CHECK(template_kind IN('expense','income'));
ALTER TABLE expenses.receipt_reviews ADD COLUMN reviewed_entry_version bigint CHECK(reviewed_entry_version>0),
 ADD COLUMN reviewed_entry_original_minor bigint CHECK(reviewed_entry_original_minor BETWEEN -1000000000000 AND 1000000000000),
 ADD COLUMN reviewed_entry_currency text,ADD COLUMN reviewed_entry_exponent smallint,ADD COLUMN entry_difference_minor numeric,
 ADD CHECK((entry_id IS NULL AND reviewed_entry_version IS NULL AND reviewed_entry_original_minor IS NULL AND reviewed_entry_currency IS NULL AND reviewed_entry_exponent IS NULL AND entry_difference_minor IS NULL)
 OR (entry_id IS NOT NULL AND reviewed_entry_version IS NOT NULL AND reviewed_entry_original_minor IS NOT NULL AND reviewed_entry_currency IS NOT NULL AND reviewed_entry_exponent IS NOT NULL));
ALTER TABLE expenses.entry_split_sets ADD UNIQUE(household_id,id,entry_id);
ALTER TABLE expenses.entry_person_shares ADD FOREIGN KEY(household_id,split_set_id,entry_id) REFERENCES expenses.entry_split_sets(household_id,id,entry_id);
ALTER TABLE expenses.receipt_review_lines DROP CONSTRAINT receipt_review_lines_household_id_review_id_position_key;
ALTER TABLE expenses.receipt_review_lines ADD CONSTRAINT receipt_line_order UNIQUE(household_id,review_id,position) DEFERRABLE INITIALLY DEFERRED;
CREATE UNIQUE INDEX finance_reopen_once ON expenses.reconciliations(household_id,reopens_id) WHERE reopens_id IS NOT NULL;

ALTER TABLE expenses.entries ADD COLUMN money_mode text NOT NULL DEFAULT 'legacy_eur' CHECK(money_mode IN('legacy_eur','foreign_original'));
ALTER TABLE expenses.entries ALTER COLUMN amount_minor DROP NOT NULL;
ALTER TABLE expenses.entries DROP CONSTRAINT entries_amount_minor_check;
ALTER TABLE expenses.entries DROP CONSTRAINT entries_check;
ALTER TABLE expenses.entries ADD CONSTRAINT finance_entry_amount CHECK(
 (money_mode='legacy_eur' AND amount_minor IS NOT NULL AND amount_minor<>0 AND amount_minor BETWEEN -1000000000000 AND 1000000000000 AND
 ((kind='expense' AND amount_minor>0 AND reverses_entry_id IS NULL) OR (kind='refund' AND amount_minor<0 AND reverses_entry_id IS NULL) OR (kind='reversal' AND reverses_entry_id IS NOT NULL))) OR
 (money_mode='foreign_original' AND (amount_minor IS NOT NULL AND amount_minor BETWEEN -1000000000000 AND 1000000000000 OR amount_minor IS NULL AND state='draft') AND ((kind IN('expense','refund') AND reverses_entry_id IS NULL) OR (kind='reversal' AND reverses_entry_id IS NOT NULL))));
ALTER TABLE expenses.entry_lines ALTER COLUMN amount_minor DROP NOT NULL;
ALTER TABLE expenses.entry_lines DROP CONSTRAINT entry_lines_amount_minor_check;
ALTER TABLE expenses.entry_lines ADD CONSTRAINT finance_line_amount CHECK(amount_minor IS NULL OR amount_minor BETWEEN -1000000000000 AND 1000000000000);

CREATE FUNCTION expenses.currency_exponent(currency text) RETURNS integer
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT CASE WHEN currency IN('EUR','USD','GBP','CHF','CAD','AUD','NZD','SEK','NOK','DKK','PLN','CZK','HUF','RON') THEN 2 WHEN currency='JPY' THEN 0 WHEN currency IN('KWD','BHD') THEN 3 ELSE NULL END
$$;
CREATE FUNCTION expenses.round_ratio(numerator numeric,denominator numeric) RETURNS numeric
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT sign(numerator)*(div(abs(numerator),denominator)+CASE WHEN mod(abs(numerator),denominator)*2>=denominator THEN 1 ELSE 0 END)
$$;
CREATE FUNCTION expenses.fx_minor(amount bigint,exponent integer,numerator numeric,denominator numeric) RETURNS bigint
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE result numeric;
BEGIN
 IF amount IS NULL OR exponent NOT IN(0,2,3) OR numerator IS NULL OR denominator IS NULL OR numerator<1 OR denominator<1 OR numerator>=1e18 OR denominator>=1e18 OR trunc(numerator)<>numerator OR trunc(denominator)<>denominator THEN RAISE EXCEPTION 'invalid exact exchange rate' USING ERRCODE='23514';END IF;
 result=expenses.round_ratio(amount::numeric*numerator*100,denominator*power(10::numeric,exponent));
 IF abs(result)>1000000000000 THEN RAISE EXCEPTION 'converted amount exceeds boundary' USING ERRCODE='23514';END IF;
 RETURN result::bigint;
END $$;

CREATE FUNCTION expenses.guard_finance_domain() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE actor uuid; relation_parent uuid; parent_state text; reference_household uuid; old_document jsonb; new_document jsonb;
BEGIN
 IF core.finance_real_maintenance() THEN
  IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN reference_household=OLD.household_id;ELSE reference_household=NEW.household_id;END IF;
 actor=core.finance_business_actor(reference_household);
 IF actor IS NULL THEN RAISE EXCEPTION 'current verified financial business actor required' USING ERRCODE='42501';END IF;
 PERFORM id FROM core.households WHERE id=reference_household FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=reference_household AND m.user_id=actor AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND (m.role IN('admin','editor') OR TG_TABLE_NAME='saved_filters')) OR NOT EXISTS(SELECT 1 FROM core.module_states WHERE household_id=reference_household AND module_id='expenses' AND state='enabled') THEN RAISE EXCEPTION 'financial actor or module is unavailable' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at) THEN RAISE EXCEPTION 'financial fact identity is immutable' USING ERRCODE='23514';END IF;
 IF TG_OP='DELETE' THEN old_document=to_jsonb(OLD);ELSE new_document=to_jsonb(NEW);END IF;
 IF TG_OP<>'INSERT' THEN old_document=to_jsonb(OLD);END IF;
 -- Reference creation and denomination edits serialize on the same account row.
 -- Include both old/new and nested template native contexts in canonical order.
 IF TG_TABLE_NAME<>'accounts' THEN
  PERFORM id FROM expenses.accounts WHERE household_id=reference_household AND id IN(
   (new_document->>'account_id')::uuid,(old_document->>'account_id')::uuid,
   (new_document->'template'->'payment'->>'account_id')::uuid,(old_document->'template'->'payment'->>'account_id')::uuid)
   ORDER BY id FOR SHARE NOWAIT;
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM unnest(ARRAY['entry_id','line_id','event_id','split_set_id','review_id','reconciliation_id','template_id']) key
  WHERE old_document ? key AND new_document->key IS DISTINCT FROM old_document->key
   AND NOT(TG_TABLE_NAME='recurring_occurrences' AND key IN('entry_id','event_id') AND old_document->>'state'='proposed'
    AND old_document->>'entry_id' IS NULL AND old_document->>'event_id' IS NULL AND new_document->>'state'='draft_created')) THEN
  RAISE EXCEPTION 'financial child parent is immutable' USING ERRCODE='23514';END IF;
 IF TG_TABLE_NAME='recurring_occurrences' THEN
  IF TG_OP='DELETE' OR TG_OP='UPDATE' AND(old_document->>'state'<>'proposed' OR new_document->>'state'<>'draft_created'
   OR (old_document-ARRAY['entry_id','event_id','state','version','changed_by','updated_at']) IS DISTINCT FROM
      (new_document-ARRAY['entry_id','event_id','state','version','changed_by','updated_at'])) THEN
   RAISE EXCEPTION 'materialized recurrence is immutable' USING ERRCODE='23514';END IF;
  IF TG_OP='INSERT' THEN
   IF new_document->>'state'<>'proposed' OR new_document->>'entry_id' IS NOT NULL OR new_document->>'event_id' IS NOT NULL THEN
    RAISE EXCEPTION 'recurrence must begin with an unbound proposal' USING ERRCODE='23514';END IF;
  ELSIF NOT EXISTS(SELECT 1 FROM expenses.recurring_templates template
   JOIN core.finance_origin_operations operation ON operation.id=nullif(current_setting('household.finance_origin',true),'')::uuid
   JOIN core.finance_origin_operations child ON child.parent_id=operation.id AND child.household_id=operation.household_id
   JOIN core.finance_origin_resources head ON head.household_id=child.household_id AND head.root_id=child.id
    AND head.resource_type=child.target_kind AND head.resource_id=child.target_id AND head.row_number=0 AND head.audit_id IS NULL AND head.retired_at IS NULL
   WHERE template.household_id=reference_household AND template.id=(new_document->>'template_id')::uuid
    AND operation.household_id=reference_household AND operation.target_kind='recurring_occurrences' AND operation.target_id=NEW.id
    AND operation.state='pending' AND operation.origin='recurrence' AND operation.transaction_id=pg_current_xact_id_if_assigned()
    AND operation.transaction_started_at=transaction_timestamp() AND operation.actor_user_id=actor
    AND child.state='complete' AND child.origin='recurrence' AND child.actor_user_id=actor
    AND child.expenses_generation=operation.expenses_generation
    AND((template.template_kind='expense' AND new_document->>'event_id' IS NULL AND child.target_kind='entries'
      AND child.target_id=(new_document->>'entry_id')::uuid AND EXISTS(SELECT 1 FROM expenses.entries e WHERE e.household_id=reference_household AND e.id=child.target_id AND e.state='draft'))
     OR(template.template_kind='income' AND new_document->>'entry_id' IS NULL AND child.target_kind='cash_events'
      AND child.target_id=(new_document->>'event_id')::uuid AND EXISTS(SELECT 1 FROM expenses.cash_events c WHERE c.household_id=reference_household AND c.id=child.target_id AND c.state='draft' AND c.event_kind='income')))) THEN
   RAISE EXCEPTION 'recurrence output requires its completed same-operation draft' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_TABLE_NAME='account_movements' THEN PERFORM id FROM expenses.accounts WHERE household_id=reference_household AND id=COALESCE((new_document->>'account_id')::uuid,(old_document->>'account_id')::uuid) FOR UPDATE NOWAIT;END IF;
 IF TG_TABLE_NAME IN('entry_finance','entry_original_lines','entry_split_sets','entry_person_shares') THEN
  relation_parent=COALESCE((new_document->>'entry_id')::uuid,(old_document->>'entry_id')::uuid);
  SELECT state INTO parent_state FROM expenses.entries WHERE household_id=reference_household AND id=relation_parent FOR UPDATE NOWAIT;
  IF parent_state='posted' THEN RAISE EXCEPTION 'posted financial context is immutable' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME IN('account_movements','settlement_details') THEN
  relation_parent=COALESCE((new_document->>'event_id')::uuid,(old_document->>'event_id')::uuid);
  SELECT state INTO parent_state FROM expenses.cash_events WHERE household_id=reference_household AND id=relation_parent FOR UPDATE NOWAIT;
  IF parent_state='posted' THEN RAISE EXCEPTION 'posted cash effects are immutable' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME='cash_events' AND TG_OP<>'INSERT' AND old_document->>'state'='posted' THEN RAISE EXCEPTION 'posted cash event is immutable' USING ERRCODE='23514';
 ELSIF TG_TABLE_NAME='entry_relations' AND TG_OP<>'INSERT' THEN RAISE EXCEPTION 'financial origin relation is immutable' USING ERRCODE='23514';
 ELSIF TG_TABLE_NAME='reconciliations' AND TG_OP<>'INSERT' AND old_document->>'state' IN('confirmed','reopened') THEN RAISE EXCEPTION 'confirmed reconciliation is immutable; append reopening' USING ERRCODE='23514';
 ELSIF TG_TABLE_NAME='goal_contributions' AND TG_OP<>'INSERT' AND old_document->>'contribution_kind'<>'planned' THEN RAISE EXCEPTION 'recorded contribution is immutable' USING ERRCODE='23514';
 ELSIF TG_TABLE_NAME='receipt_reviews' AND TG_OP<>'INSERT' AND old_document->>'state' IN('confirmed','superseded') THEN RAISE EXCEPTION 'confirmed receipt review is immutable' USING ERRCODE='23514';
 ELSIF TG_TABLE_NAME='reconciliation_items' THEN
  relation_parent=COALESCE((new_document->>'reconciliation_id')::uuid,(old_document->>'reconciliation_id')::uuid);
  SELECT state INTO parent_state FROM expenses.reconciliations WHERE household_id=reference_household AND id=relation_parent FOR UPDATE NOWAIT;
  IF parent_state<>'review' THEN RAISE EXCEPTION 'confirmed reconciliation items are immutable' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME='receipt_review_lines' THEN
  relation_parent=COALESCE((new_document->>'review_id')::uuid,(old_document->>'review_id')::uuid);
  SELECT state INTO parent_state FROM expenses.receipt_reviews WHERE household_id=reference_household AND id=relation_parent FOR UPDATE NOWAIT;
  IF parent_state<>'draft' THEN RAISE EXCEPTION 'confirmed receipt evidence is immutable' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_TABLE_NAME='accounts' AND TG_OP='UPDATE' AND (new_document->>'currency_code' IS DISTINCT FROM old_document->>'currency_code' OR new_document->>'currency_exponent' IS DISTINCT FROM old_document->>'currency_exponent') AND (
 EXISTS(SELECT 1 FROM expenses.account_movements WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.entry_finance WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.cash_events WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.bank_profiles WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.recurring_templates WHERE household_id=reference_household AND (account_id=OLD.id OR template->'payment'->>'account_id'=OLD.id::text))
 OR EXISTS(SELECT 1 FROM expenses.savings_goals WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.categorization_rules WHERE household_id=reference_household AND account_id=OLD.id)
 OR EXISTS(SELECT 1 FROM expenses.reconciliations WHERE household_id=reference_household AND account_id=OLD.id)
 ) THEN RAISE EXCEPTION 'account denomination is immutable after native context reference' USING ERRCODE='23514';END IF;
 IF TG_TABLE_NAME='recurring_templates' AND TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM expenses.recurring_occurrences WHERE household_id=reference_household AND template_id=OLD.id) AND (new_document->'anchor_on' IS DISTINCT FROM old_document->'anchor_on' OR new_document->'repeat_unit' IS DISTINCT FROM old_document->'repeat_unit' OR new_document->'repeat_interval' IS DISTINCT FROM old_document->'repeat_interval' OR new_document->'monthday_policy' IS DISTINCT FROM old_document->'monthday_policy') THEN RAISE EXCEPTION 'materialized recurrence schedule is immutable; clone a new template' USING ERRCODE='23514';END IF;
 IF TG_TABLE_NAME IN('saved_filters','bank_profiles') THEN
  IF TG_OP='INSERT' AND NEW.owner_user_id<>actor OR TG_OP<>'INSERT' AND (old_document->>'owner_user_id') IS DISTINCT FROM actor::text OR TG_OP='UPDATE' AND NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id THEN RAISE EXCEPTION 'private preference owner required' USING ERRCODE='42501';END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 IF new_document ? 'currency_code' AND ((new_document->>'currency_exponent') IS NOT NULL) AND expenses.currency_exponent(new_document->>'currency_code') IS DISTINCT FROM (new_document->>'currency_exponent')::integer THEN RAISE EXCEPTION 'currency exponent does not match catalogue' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' THEN NEW.version=1;NEW.created_by=actor;NEW.created_at=clock_timestamp();ELSE NEW.version=OLD.version+1;END IF;
 NEW.changed_by=actor;NEW.updated_at=clock_timestamp();RETURN NEW;
END $$;

CREATE FUNCTION expenses.touch_finance_entry() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid; owner_household uuid;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_OP='DELETE' THEN parent_id=OLD.entry_id;owner_household=OLD.household_id;ELSE parent_id=NEW.entry_id;owner_household=NEW.household_id;END IF;
 UPDATE expenses.entries SET version=version+1,updated_at=clock_timestamp() WHERE household_id=owner_household AND id=parent_id;
 RETURN NULL;
END $$;

CREATE FUNCTION expenses.touch_receipt_review() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid; owner_household uuid;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_OP='DELETE' THEN parent_id=OLD.review_id;owner_household=OLD.household_id;ELSE parent_id=NEW.review_id;owner_household=NEW.household_id;END IF;
 UPDATE expenses.receipt_reviews SET version=version+1,updated_at=clock_timestamp() WHERE household_id=owner_household AND id=parent_id;
 RETURN NULL;
END $$;

-- Preserve the released editor ABI while distinguishing genuine maintenance
-- from an Owner SECURITY DEFINER frame reached by the application role.
CREATE OR REPLACE FUNCTION expenses.protect_posted() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid;parent_household uuid;parent_state text;
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF TG_TABLE_NAME='entries' THEN
  IF OLD.state='posted' THEN RAISE EXCEPTION 'posted entry is immutable' USING ERRCODE='23514';END IF;
  IF TG_OP='UPDATE' AND(NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id) THEN
   RAISE EXCEPTION 'entry identity is immutable' USING ERRCODE='23514';END IF;
 ELSE
  IF TG_OP='UPDATE' AND(NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.entry_id IS DISTINCT FROM OLD.entry_id) THEN
   RAISE EXCEPTION 'allocation identity and parent are immutable' USING ERRCODE='23514';END IF;
  IF TG_OP='DELETE' THEN parent_id=OLD.entry_id;parent_household=OLD.household_id;ELSE parent_id=NEW.entry_id;parent_household=NEW.household_id;END IF;
  SELECT state INTO parent_state FROM expenses.entries WHERE household_id=parent_household AND id=parent_id FOR UPDATE NOWAIT;
  IF NOT FOUND AND TG_OP<>'DELETE' THEN RAISE EXCEPTION 'allocation parent missing' USING ERRCODE='23503';END IF;
  IF parent_state='posted' THEN RAISE EXCEPTION 'posted allocation is immutable' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION expenses.touch_entry_after_line() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid;parent_household uuid;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_OP='DELETE' THEN parent_id=OLD.entry_id;parent_household=OLD.household_id;ELSE parent_id=NEW.entry_id;parent_household=NEW.household_id;END IF;
 UPDATE expenses.entries SET version=version+1,updated_at=clock_timestamp() WHERE household_id=parent_household AND id=parent_id;
 RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION expenses.validate_total() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid; owner_household uuid; e expenses.entries%ROWTYPE; f expenses.entry_finance%ROWTYPE; original_total numeric; base_total numeric; line_count integer; calculated bigint; known_rate boolean;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='entries' THEN
  IF TG_OP='DELETE' THEN parent_id=OLD.id;owner_household=OLD.household_id;ELSE parent_id=NEW.id;owner_household=NEW.household_id;END IF;
 ELSE
  IF TG_OP='DELETE' THEN parent_id=OLD.entry_id;owner_household=OLD.household_id;ELSE parent_id=NEW.entry_id;owner_household=NEW.household_id;END IF;
 END IF;
 SELECT * INTO e FROM expenses.entries WHERE household_id=owner_household AND id=parent_id;
 IF NOT FOUND THEN RETURN NULL;END IF;
 SELECT * INTO f FROM expenses.entry_finance WHERE household_id=owner_household AND entry_id=parent_id;
 IF e.money_mode='legacy_eur' THEN
  IF e.amount_minor IS NULL OR e.amount_minor=0 OR EXISTS(SELECT 1 FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=parent_id AND (amount_minor IS NULL OR amount_minor=0)) OR EXISTS(SELECT 1 FROM expenses.entry_original_lines o JOIN expenses.entry_lines l ON l.household_id=o.household_id AND l.id=o.line_id WHERE o.household_id=owner_household AND o.entry_id=parent_id AND (f.original_state IS DISTINCT FROM 'known' OR f.original_currency IS DISTINCT FROM 'EUR' OR o.original_minor IS DISTINCT FROM l.amount_minor)) THEN RAISE EXCEPTION 'legacy EUR cannot use unknown or zero money' USING ERRCODE='23514';END IF;
  IF f.id IS NOT NULL AND f.original_state='known' AND (f.original_currency IS DISTINCT FROM 'EUR' OR f.original_exponent IS DISTINCT FROM 2 OR f.original_minor IS DISTINCT FROM e.amount_minor OR f.fx_numerator IS NOT NULL OR f.fx_denominator IS NOT NULL) THEN RAISE EXCEPTION 'EUR context conflicts with booking' USING ERRCODE='23514';END IF;
 ELSE
  IF f.id IS NULL OR f.original_state<>'known' OR f.original_currency='EUR' OR expenses.currency_exponent(f.original_currency) IS DISTINCT FROM f.original_exponent::integer OR f.original_minor IS NULL OR f.original_minor=0 OR e.kind='expense' AND f.original_minor<0 OR e.kind='refund' AND f.original_minor>0 THEN RAISE EXCEPTION 'complete original foreign money required' USING ERRCODE='23514';END IF;
  IF EXISTS(SELECT 1 FROM expenses.entry_lines l WHERE l.household_id=owner_household AND l.entry_id=parent_id AND NOT EXISTS(SELECT 1 FROM expenses.entry_original_lines o WHERE o.household_id=l.household_id AND o.line_id=l.id AND o.entry_id=l.entry_id)) OR EXISTS(SELECT 1 FROM expenses.entry_original_lines o JOIN expenses.entry_lines l ON l.household_id=o.household_id AND l.id=o.line_id WHERE o.household_id=owner_household AND o.entry_id=parent_id AND l.entry_id<>parent_id) THEN RAISE EXCEPTION 'complete original category lines required' USING ERRCODE='23514';END IF;
  SELECT count(*),coalesce(sum(o.original_minor),0),sum(l.amount_minor) INTO line_count,original_total,base_total FROM expenses.entry_lines l JOIN expenses.entry_original_lines o ON o.household_id=l.household_id AND o.line_id=l.id WHERE l.household_id=owner_household AND l.entry_id=parent_id;
  IF line_count>100 THEN RAISE EXCEPTION 'foreign allocation exceeds line boundary' USING ERRCODE='23514';END IF;
  known_rate=f.fx_numerator IS NOT NULL AND f.fx_denominator IS NOT NULL AND f.fx_reviewed_by IS NOT NULL AND f.fx_reviewed_at IS NOT NULL AND f.fx_effective_on IS NOT NULL AND f.fx_source IS NOT NULL AND length(f.fx_source) BETWEEN 1 AND 200;
  IF NOT known_rate THEN
   IF e.state<>'draft' OR e.amount_minor IS NOT NULL OR f.fx_numerator IS NOT NULL OR f.fx_denominator IS NOT NULL OR f.fx_reviewed_by IS NOT NULL OR f.fx_reviewed_at IS NOT NULL OR f.fx_effective_on IS NOT NULL OR f.fx_source IS NOT NULL OR EXISTS(SELECT 1 FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=parent_id AND amount_minor IS NOT NULL) THEN RAISE EXCEPTION 'unknown rate is a nullable foreign draft only' USING ERRCODE='23514';END IF;
  ELSE
   calculated=expenses.fx_minor(f.original_minor,f.original_exponent,f.fx_numerator,f.fx_denominator);
   IF e.amount_minor IS DISTINCT FROM calculated OR EXISTS(SELECT 1 FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=parent_id AND amount_minor IS NULL) THEN RAISE EXCEPTION 'exact foreign base does not match approved rate' USING ERRCODE='23514';END IF;
   IF e.kind<>'reversal' AND EXISTS(WITH source AS(SELECT l.id,l.amount_minor,o.original_minor::numeric*f.fx_numerator*100 n,f.fx_denominator*power(10::numeric,f.original_exponent) d FROM expenses.entry_lines l JOIN expenses.entry_original_lines o ON o.household_id=l.household_id AND o.line_id=l.id WHERE l.household_id=owner_household AND l.entry_id=parent_id),quotas AS(SELECT *,div(n,d)-CASE WHEN n<0 AND mod(n,d)<>0 THEN 1 ELSE 0 END flr FROM source),ranked AS(SELECT *,row_number() OVER(ORDER BY n-flr*d DESC,id) rank,sum(flr) OVER() floor_sum,sum(n) OVER() numerator_sum FROM quotas) SELECT 1 FROM ranked WHERE amount_minor IS DISTINCT FROM flr+CASE WHEN rank<=expenses.round_ratio(numerator_sum,d)-floor_sum THEN 1 ELSE 0 END) THEN RAISE EXCEPTION 'foreign category rounding is not deterministic' USING ERRCODE='23514';END IF;
  END IF;
  IF e.state='posted' AND (line_count=0 OR original_total IS DISTINCT FROM f.original_minor::numeric OR base_total IS DISTINCT FROM e.amount_minor::numeric) THEN RAISE EXCEPTION 'posted original and base allocations must be exact' USING ERRCODE='23514';END IF;
 END IF;
 IF e.state='posted' THEN
  IF e.amount_minor IS NULL THEN RAISE EXCEPTION 'posted base is unknown' USING ERRCODE='23514';END IF;
  SELECT coalesce(sum(amount_minor),0),count(*) INTO base_total,line_count FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=parent_id;
  IF line_count=0 OR base_total IS DISTINCT FROM e.amount_minor::numeric THEN RAISE EXCEPTION 'posted allocation total mismatch' USING ERRCODE='23514';END IF;
  IF e.kind='reversal' AND NOT EXISTS(SELECT 1 FROM expenses.entries original WHERE original.household_id=owner_household AND original.id=e.reverses_entry_id AND original.state='posted' AND e.amount_minor=-original.amount_minor AND e.money_mode=original.money_mode) THEN RAISE EXCEPTION 'reversal must negate original known base' USING ERRCODE='23514';END IF;
  IF e.kind='reversal' AND f.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM expenses.entries original LEFT JOIN expenses.entry_finance original_money ON original_money.household_id=original.household_id AND original_money.entry_id=original.id WHERE original.household_id=owner_household AND original.id=e.reverses_entry_id AND f.original_currency IS NOT DISTINCT FROM coalesce(original_money.original_currency,'EUR') AND f.original_exponent IS NOT DISTINCT FROM coalesce(original_money.original_exponent,2) AND f.original_minor IS NOT DISTINCT FROM -coalesce(original_money.original_minor,original.amount_minor) AND f.fx_numerator IS NOT DISTINCT FROM original_money.fx_numerator AND f.fx_denominator IS NOT DISTINCT FROM original_money.fx_denominator AND f.fx_effective_on IS NOT DISTINCT FROM original_money.fx_effective_on AND f.fx_source IS NOT DISTINCT FROM original_money.fx_source AND f.account_id IS NOT DISTINCT FROM original_money.account_id AND f.payment_minor IS NOT DISTINCT FROM -original_money.payment_minor AND f.payment_base_minor IS NOT DISTINCT FROM -original_money.payment_base_minor AND e.paid_by_membership_id IS NOT DISTINCT FROM original.paid_by_membership_id) THEN RAISE EXCEPTION 'reversal must negate frozen original, payer and payment' USING ERRCODE='23514';END IF;
  IF e.kind='reversal' AND EXISTS((SELECT category_id,amount_minor,note,position FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=e.id EXCEPT ALL SELECT category_id,-amount_minor,note,position FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=e.reverses_entry_id) UNION ALL (SELECT category_id,-amount_minor,note,position FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=e.reverses_entry_id EXCEPT ALL SELECT category_id,amount_minor,note,position FROM expenses.entry_lines WHERE household_id=owner_household AND entry_id=e.id)) THEN RAISE EXCEPTION 'reversal category facts must exactly negate original' USING ERRCODE='23514';END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM expenses.entry_split_sets s WHERE s.household_id=owner_household AND s.entry_id=parent_id AND s.active AND (s.total_minor IS DISTINCT FROM CASE WHEN f.original_state='known' THEN f.original_minor ELSE e.amount_minor END OR s.currency_code IS DISTINCT FROM CASE WHEN f.original_state='known' THEN f.original_currency ELSE 'EUR' END OR (SELECT count(*) FROM expenses.entry_person_shares p WHERE p.household_id=s.household_id AND p.split_set_id=s.id)<>jsonb_array_length(s.basis) OR (SELECT coalesce(sum(p.share_minor),0) FROM expenses.entry_person_shares p WHERE p.household_id=s.household_id AND p.split_set_id=s.id) IS DISTINCT FROM s.total_minor::numeric OR EXISTS(SELECT 1 FROM expenses.entry_person_shares p WHERE p.household_id=s.household_id AND p.split_set_id=s.id AND (p.entry_id<>s.entry_id OR p.share_minor*s.total_minor::numeric<0)))) THEN RAISE EXCEPTION 'person allocation does not match original fact' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;


CREATE FUNCTION expenses.validate_cash_event() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE event_key uuid; owner_household uuid; e expenses.cash_events%ROWTYPE; movement_count integer; distinct_accounts integer; native_sum numeric; expected bigint; first_currency text;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='cash_events' THEN IF TG_OP='DELETE' THEN event_key=OLD.id;owner_household=OLD.household_id;ELSE event_key=NEW.id;owner_household=NEW.household_id;END IF;
 ELSE IF TG_OP='DELETE' THEN event_key=OLD.event_id;owner_household=OLD.household_id;ELSE event_key=NEW.event_id;owner_household=NEW.household_id;END IF;END IF;
 SELECT * INTO e FROM expenses.cash_events WHERE household_id=owner_household AND id=event_key;
 IF NOT FOUND THEN RETURN NULL;END IF;
 IF expenses.currency_exponent(e.original_currency) IS DISTINCT FROM e.original_exponent::integer THEN RAISE EXCEPTION 'cash original denomination is invalid' USING ERRCODE='23514';END IF;
 IF e.event_kind='income' AND (e.entry_id IS NOT NULL OR (e.account_id IS NULL) IS DISTINCT FROM (e.payment_context IS NULL)
 OR e.payment_context IS NOT NULL AND (e.payment_context->>'account_id' IS DISTINCT FROM e.account_id::text
 OR jsonb_typeof(e.payment_context->'native_minor') IS DISTINCT FROM 'string' OR (e.payment_context->>'native_minor') !~ '^[1-9][0-9]{0,12}$')) THEN
 RAISE EXCEPTION 'income account and reviewed payment context disagree' USING ERRCODE='23514';END IF;
 SELECT count(*),count(DISTINCT account_id),sum(amount_minor),min(currency_code) INTO movement_count,distinct_accounts,native_sum,first_currency FROM expenses.account_movements WHERE household_id=owner_household AND event_id=event_key;
 IF e.original_currency='EUR' THEN
  IF e.base_minor IS DISTINCT FROM e.original_minor OR e.fx_numerator IS NOT NULL OR e.fx_denominator IS NOT NULL THEN RAISE EXCEPTION 'cash EUR valuation must be exact' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='opening' AND e.original_minor=0 AND e.fx_numerator IS NULL AND e.fx_denominator IS NULL THEN
  IF e.base_minor IS DISTINCT FROM 0 OR e.fx_effective_on IS NOT NULL OR e.fx_source IS NOT NULL THEN RAISE EXCEPTION 'explicit zero opening cannot imply a fabricated rate' USING ERRCODE='23514';END IF;
 ELSIF e.fx_numerator IS NULL AND e.fx_denominator IS NULL AND e.state='draft' THEN
  IF e.base_minor IS NOT NULL OR e.fx_effective_on IS NOT NULL OR e.fx_source IS NOT NULL THEN RAISE EXCEPTION 'unknown draft rate cannot imply a value' USING ERRCODE='23514';END IF;
 ELSE
  expected=expenses.fx_minor(e.original_minor,e.original_exponent,e.fx_numerator,e.fx_denominator);
  IF e.base_minor IS DISTINCT FROM expected OR e.fx_effective_on IS NULL OR e.fx_source IS NULL OR length(e.fx_source) NOT BETWEEN 1 AND 200 THEN RAISE EXCEPTION 'cash valuation is not approved' USING ERRCODE='23514';END IF;
 END IF;
 IF e.state='draft' THEN
  IF movement_count<>0 THEN RAISE EXCEPTION 'draft cash has no committed movements' USING ERRCODE='23514';END IF;
  RETURN NULL;
 END IF;
 IF e.base_minor IS NULL THEN RAISE EXCEPTION 'posted cash valuation is unknown' USING ERRCODE='23514';END IF;
 IF EXISTS(SELECT 1 FROM expenses.account_movements m JOIN expenses.accounts a ON a.household_id=m.household_id AND a.id=m.account_id WHERE m.household_id=owner_household AND m.event_id=event_key AND (
 m.currency_code IS DISTINCT FROM a.currency_code OR m.currency_exponent IS DISTINCT FROM a.currency_exponent OR m.amount_minor=0 AND e.event_kind<>'opening'
 OR CASE WHEN m.currency_code='EUR' THEN m.base_minor IS DISTINCT FROM m.amount_minor OR m.fx_numerator IS NOT NULL OR m.fx_denominator IS NOT NULL
 WHEN e.event_kind='opening' AND m.amount_minor=0 AND m.fx_numerator IS NULL AND m.fx_denominator IS NULL THEN m.base_minor IS DISTINCT FROM 0 OR m.fx_effective_on IS NOT NULL OR m.fx_source IS NOT NULL
 ELSE m.base_minor IS DISTINCT FROM expenses.fx_minor(m.amount_minor,m.currency_exponent,m.fx_numerator,m.fx_denominator) OR m.fx_effective_on IS NULL OR m.fx_source IS NULL OR length(m.fx_source) NOT BETWEEN 1 AND 200 END
 )) THEN RAISE EXCEPTION 'account movement denomination or exact valuation differs' USING ERRCODE='23514';END IF;
 IF EXISTS(SELECT 1 FROM expenses.account_movements m JOIN expenses.cash_events opening ON opening.household_id=m.household_id AND opening.account_id=m.account_id AND opening.event_kind='opening' AND opening.state='posted' WHERE m.household_id=owner_household AND m.event_id=event_key AND e.occurred_on<opening.occurred_on) THEN RAISE EXCEPTION 'posting predates reviewed opening balance' USING ERRCODE='23514';END IF;
 IF e.event_kind IN('expense','refund','reversal') THEN
  IF e.entry_id IS NULL OR NOT EXISTS(SELECT 1 FROM expenses.entries p WHERE p.household_id=owner_household AND p.id=e.entry_id AND p.state='posted' AND p.kind=e.event_kind AND p.amount_minor IS NOT DISTINCT FROM e.base_minor) OR movement_count>1 THEN RAISE EXCEPTION 'cash consumption linkage differs from immutable booking' USING ERRCODE='23514';END IF;
  IF EXISTS(SELECT 1 FROM expenses.entry_finance f WHERE f.household_id=owner_household AND f.entry_id=e.entry_id AND ((f.account_id IS NULL AND movement_count<>0) OR (f.account_id IS NOT NULL AND (movement_count<>1 OR NOT EXISTS(SELECT 1 FROM expenses.account_movements m WHERE m.household_id=owner_household AND m.event_id=e.id AND m.account_id=f.account_id AND m.amount_minor IS NOT DISTINCT FROM f.payment_minor AND m.base_minor IS NOT DISTINCT FROM f.payment_base_minor))))) THEN RAISE EXCEPTION 'account effects do not match frozen payment' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='income' THEN
  IF movement_count<>1 OR e.original_minor<=0 OR native_sum<=0 OR e.account_id IS NULL OR NOT EXISTS(SELECT 1 FROM expenses.account_movements m WHERE m.household_id=owner_household AND m.event_id=e.id AND m.account_id=e.account_id AND m.amount_minor::text=e.payment_context->>'native_minor' AND (m.base_minor=e.base_minor OR coalesce((e.payment_context->>'difference_reviewed')::boolean,false))) THEN RAISE EXCEPTION 'income must have one actual reviewed credit' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='opening' THEN
  IF movement_count<>1 OR NOT EXISTS(SELECT 1 FROM expenses.account_movements m WHERE m.household_id=owner_household AND m.event_id=e.id AND m.account_id=e.account_id AND m.amount_minor=e.original_minor) THEN RAISE EXCEPTION 'opening needs one explicit native balance' USING ERRCODE='23514';END IF;
  PERFORM id FROM expenses.accounts WHERE household_id=owner_household AND id=e.account_id FOR UPDATE NOWAIT;
  IF (SELECT count(*) FROM expenses.cash_events WHERE household_id=owner_household AND account_id=e.account_id AND event_kind='opening' AND state='posted')<>1 THEN RAISE EXCEPTION 'duplicate opening balance' USING ERRCODE='23514';END IF;
  IF EXISTS(SELECT 1 FROM expenses.account_movements m JOIN expenses.cash_events prior ON prior.household_id=m.household_id AND prior.id=m.event_id WHERE m.household_id=owner_household AND m.account_id=e.account_id AND prior.state='posted' AND prior.occurred_on<e.occurred_on) THEN RAISE EXCEPTION 'opening must precede existing posted movements' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='adjustment' THEN
  IF e.original_minor=0 OR movement_count<>1 OR NOT EXISTS(SELECT 1 FROM expenses.account_movements m WHERE m.household_id=owner_household AND m.event_id=e.id AND m.account_id=e.account_id AND m.amount_minor=e.original_minor AND m.base_minor=e.base_minor AND m.currency_code=e.original_currency) THEN RAISE EXCEPTION 'explicit adjustment needs one exact nonzero native effect' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='transfer' THEN
  IF movement_count<>2 OR distinct_accounts<>2 OR (SELECT count(*) FROM expenses.account_movements WHERE household_id=owner_household AND event_id=event_key AND amount_minor<0)<>1 OR (SELECT count(DISTINCT currency_code) FROM expenses.account_movements WHERE household_id=owner_household AND event_id=event_key)=1 AND (native_sum<>0 OR (SELECT sum(base_minor) FROM expenses.account_movements WHERE household_id=owner_household AND event_id=event_key)<>0) THEN RAISE EXCEPTION 'transfer requires exact opposing native legs' USING ERRCODE='23514';END IF;
 ELSIF e.event_kind='settlement' THEN
  IF movement_count>2 OR e.original_minor<=0 OR NOT EXISTS(SELECT 1 FROM expenses.settlement_details s WHERE s.household_id=owner_household AND s.event_id=e.id AND s.currency_code=e.original_currency AND s.currency_exponent=e.original_exponent AND s.amount_minor=e.original_minor) THEN RAISE EXCEPTION 'settlement original debt fact is incomplete' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END $$;

DO $guards$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations','entry_split_sets','entry_person_shares','settlement_details','saved_filters','category_budgets','savings_goals','goal_contributions','recurring_templates','recurring_occurrences','categorization_rules','bank_profiles','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines'] LOOP
  EXECUTE format('CREATE TRIGGER finance_domain_change BEFORE INSERT OR UPDATE OR DELETE ON expenses.%I FOR EACH ROW EXECUTE FUNCTION expenses.guard_finance_domain()',relation);
 END LOOP;
 FOREACH relation IN ARRAY ARRAY['entry_finance','entry_original_lines','entry_split_sets','entry_person_shares'] LOOP
  EXECUTE format('CREATE TRIGGER finance_parent_touch AFTER INSERT OR UPDATE OR DELETE ON expenses.%I FOR EACH ROW EXECUTE FUNCTION expenses.touch_finance_entry()',relation);
  EXECUTE format('CREATE CONSTRAINT TRIGGER finance_entry_complete AFTER INSERT OR UPDATE OR DELETE ON expenses.%I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION expenses.validate_total()',relation);
 END LOOP;
 FOREACH relation IN ARRAY ARRAY['cash_events','account_movements','settlement_details'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER finance_cash_complete AFTER INSERT OR UPDATE OR DELETE ON expenses.%I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION expenses.validate_cash_event()',relation);
 END LOOP;
END $guards$;
CREATE TRIGGER finance_receipt_touch AFTER INSERT OR UPDATE OR DELETE ON expenses.receipt_review_lines FOR EACH ROW EXECUTE FUNCTION expenses.touch_receipt_review();

REVOKE ALL ON ALL TABLES IN SCHEMA expenses FROM PUBLIC;
GRANT SELECT,INSERT,UPDATE,DELETE ON expenses.accounts,expenses.entry_finance,expenses.entry_original_lines,expenses.cash_events,expenses.account_movements,expenses.entry_relations,expenses.entry_split_sets,expenses.entry_person_shares,expenses.settlement_details,expenses.saved_filters,expenses.category_budgets,expenses.savings_goals,expenses.goal_contributions,expenses.recurring_templates,expenses.recurring_occurrences,expenses.categorization_rules,expenses.bank_profiles,expenses.reconciliations,expenses.reconciliation_items,expenses.receipt_reviews,expenses.receipt_review_lines TO household_app;
REVOKE TRUNCATE,TRIGGER ON ALL TABLES IN SCHEMA expenses FROM household_app;
REVOKE ALL ON FUNCTION expenses.currency_exponent(text),expenses.round_ratio(numeric,numeric),expenses.fx_minor(bigint,integer,numeric,numeric),expenses.guard_finance_domain(),expenses.touch_finance_entry(),expenses.touch_receipt_review(),expenses.validate_total(),expenses.validate_cash_event() FROM PUBLIC,household_app;
GRANT EXECUTE ON FUNCTION expenses.currency_exponent(text),expenses.round_ratio(numeric,numeric),expenses.fx_minor(bigint,integer,numeric,numeric) TO household_app;

-- Freeze the same CHECK deparse that pg_dump/restore will reconstruct. Only
-- this migration's new tables and its three changed legacy checks participate.
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT c.conrelid::regclass relation,c.conname,pg_get_constraintdef(c.oid) definition,c.convalidated
 FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
 WHERE c.contype='c' AND n.nspname='expenses' AND(t.relname IN(
 'accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations','entry_split_sets','entry_person_shares','settlement_details',
 'saved_filters','category_budgets','savings_goals','goal_contributions','recurring_templates','recurring_occurrences','categorization_rules','bank_profiles','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines')
 OR t.relname IN('entries','entry_lines') AND c.conname IN('finance_entry_amount','finance_line_amount','entries_money_mode_check'))
 ORDER BY c.conrelid,c.conname LOOP
  IF NOT item.convalidated THEN RAISE EXCEPTION 'Finance CHECK must be validated before capture';END IF;
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION expenses.finance_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,pg_get_expr(d.adbin,d.adrelid)) ORDER BY c.relname,a.attnum) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations'),
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,pg_get_userbyid(c.relowner),c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,coalesce(c.relacl,acldefault('r',c.relowner))::text,(SELECT coalesce(jsonb_agg(jsonb_build_array(a.attname,coalesce(a.attacl,'{}'::aclitem[])::text) ORDER BY a.attnum),'[]') FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)) ORDER BY c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations'),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,x.conname,pg_get_constraintdef(x.oid),x.convalidated) ORDER BY c.relname,x.conname) FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='expenses' AND c.relname<>'schema_migrations'),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(c.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY c.relname) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='expenses' AND c.relname<>'schema_migrations_pkey'),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE n.nspname='expenses' AND pn.nspname='expenses' AND t.tgname IN('finance_domain_change','finance_parent_touch','finance_entry_complete','finance_cash_complete','finance_receipt_touch','entries_immutable','lines_immutable','lines_touch_entry','entries_total','lines_total') AND NOT t.tgisinternal),
 'functions',(SELECT jsonb_agg(pg_get_functiondef(p.oid) ORDER BY p.proname) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='expenses' AND p.proname IN('currency_exponent','round_ratio','fx_minor','guard_finance_domain','touch_finance_entry','touch_receipt_review','protect_posted','touch_entry_after_line','validate_total','validate_cash_event')))
$$;
REVOKE ALL ON FUNCTION expenses.finance_catalog() FROM PUBLIC,household_app;
DO $freeze$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=expenses.finance_catalog();
 catalog_hash=md5(pg_get_functiondef('expenses.finance_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION expenses.finance_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT expenses.editing_ready() AND expenses.finance_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('expenses.finance_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations' AND has_table_privilege('household_app',c.oid,'TRUNCATE,TRIGGER'))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN(VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE')) required(privilege) WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations' AND NOT has_table_privilege('household_app',c.oid,required.privilege))
 AND NOT EXISTS(SELECT 1 FROM(VALUES('expenses.guard_finance_domain()'),('expenses.touch_finance_entry()'),('expenses.touch_receipt_review()'),('expenses.validate_total()'),('expenses.validate_cash_event()'),('expenses.finance_catalog()')) forbidden(fn) WHERE has_function_privilege('household_app',forbidden.fn,'EXECUTE'))
 $body$$definition$,expected::text,catalog_hash);
END $freeze$;
REVOKE ALL ON FUNCTION expenses.finance_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION expenses.finance_ready() TO household_app;
COMMIT;

