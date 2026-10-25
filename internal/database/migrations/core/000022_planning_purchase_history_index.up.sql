BEGIN;
CREATE INDEX planning_purchase_meal_history ON core.planning_purchase_requirements(household_id,mealplan_generation,meal_id);
COMMIT;
