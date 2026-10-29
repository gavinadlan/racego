DROP TABLE IF EXISTS org_documents;
DROP TABLE IF EXISTS org_verifications;
ALTER TABLE users DROP COLUMN IF EXISTS is_super_admin;
