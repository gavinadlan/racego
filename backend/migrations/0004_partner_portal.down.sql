DROP INDEX IF EXISTS organizations_created_by_idx;
ALTER TABLE organizations DROP COLUMN IF EXISTS created_by;
DROP INDEX IF EXISTS ticket_categories_bib_prefix_uniq;
