-- OPERATOR SYNTHETIC RECOVERY FIXTURE ONLY; never an application producer.
-- Run inside one existing owner transaction after the existing Core25 recovery
-- fixture has a live parent+line and a retired line+policy in the same generation.
-- Parent supplies three SET LOCAL values before this block:
-- household.fixture_household, household.fixture_actor, household.fixture_generation.
-- These values locate fixtures; they confer no maintenance authority.
-- Opaque artifact locators deliberately have no payload FK. They model retained
-- denied history, not reconstructed origins or authenticated application access.
DO $fixture$
DECLARE
 h uuid:=current_setting('household.fixture_household')::uuid;
 actor uuid:=current_setting('household.fixture_actor')::uuid;
 generation bigint:=current_setting('household.fixture_generation')::bigint;
 live_subject core.financial_subjects%ROWTYPE;
 retired_subject core.financial_subjects%ROWTYPE;
 result_subject core.financial_subjects%ROWTYPE;
 financial_set uuid:='fe260000-0000-4000-8000-000000000001';
 private_set uuid:='fe260000-0000-4000-8000-000000000002';
 unresolved_set uuid:='fe260000-0000-4000-8000-000000000003';
 attachment_object uuid:='fe260000-0000-4000-8000-000000000011';
 import_object uuid:='fe260000-0000-4000-8000-000000000012';
 first_scope uuid:='fe260000-0000-4000-8000-000000000021';
BEGIN
 SELECT s.* INTO STRICT live_subject FROM core.financial_subjects s JOIN core.financial_policies p ON p.id=s.policy_id
 WHERE s.household_id=h AND s.module_generation=generation AND s.resource_type='entries' AND s.retired_at IS NULL AND p.retired_at IS NULL
 ORDER BY s.id LIMIT 1;
 SELECT s.* INTO STRICT retired_subject FROM core.financial_subjects s JOIN core.financial_policies p ON p.id=s.policy_id
 WHERE s.household_id=h AND s.module_generation=generation AND s.resource_type='entry_lines' AND s.retired_at IS NOT NULL AND p.retired_at IS NOT NULL
 ORDER BY s.id LIMIT 1;
 SELECT s.* INTO STRICT result_subject FROM core.financial_subjects s WHERE s.household_id=h AND s.module_generation=generation
 AND s.resource_type='entry_lines' AND s.retired_at IS NULL AND s.policy_id=live_subject.policy_id ORDER BY s.id LIMIT 1;

 INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,creator_user_id,reason_code,created_at) VALUES
 (financial_set,h,'financial',2,NULL,'financial_sources','2001-02-03T04:05:06Z'),
 (private_set,h,'private_import',0,actor,'private_import_preview','2001-02-03T04:05:07Z'),
 (unresolved_set,h,'unresolved',0,NULL,'unresolved_missing_origin','2001-02-03T04:05:08Z');
 INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id) VALUES
 (h,financial_set,live_subject.id,live_subject.policy_id),(h,financial_set,retired_subject.id,retired_subject.policy_id);

 INSERT INTO core.financial_artifact_scope_versions(id,household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation,created_at)
 VALUES(first_scope,h,'attachment',attachment_object,1,financial_set,'expenses',generation,'2001-02-03T04:05:09Z');
 INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,previous_id,set_id,source_module,source_generation,created_at)
 VALUES(h,'attachment',attachment_object,2,first_scope,financial_set,'expenses',generation,'1999-01-01T00:00:00Z');
 INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation) VALUES
 (h,'capture','fe260000-0000-4000-8000-000000000013',1,financial_set,'expenses',generation),
 (h,'financial_subject',result_subject.id,1,financial_set,'expenses',generation),
 (h,'import_batch',import_object,1,private_set,'expenses',generation),
 (h,'attachment_derivative','fe260000-0000-4000-8000-000000000014',1,unresolved_set,NULL,NULL);
 INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,row_number,revision,set_id,source_module,source_generation)
 VALUES(h,'import_row',import_object,501,1,private_set,'expenses',generation);
 INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,audit_id,revision,set_id,source_module,source_generation)
 VALUES(h,'audit',9007199254740993,1,financial_set,'expenses',generation);
END $fixture$;

-- Parent compares full before/after JSON, not just this structural census:
-- sets3, members2, scopes8; one live+one retired original, exact subject/policy
-- pair retained; financial_subject target's own anchor absent from inheritedset;
-- bigint9007199254740993; composite(row501); attachment latest revision2 despite
-- earlier timestamp; exact IDs/classifications/timestamps/generations unchanged.
-- Keep all three table snapshots ORDER BY primary keys and assert readiness.
