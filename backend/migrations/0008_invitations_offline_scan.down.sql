DROP TRIGGER IF EXISTS tickets_touch ON tickets;
DROP FUNCTION IF EXISTS tickets_touch();
DROP INDEX IF EXISTS tickets_event_updated_idx;
ALTER TABLE tickets DROP COLUMN IF EXISTS updated_at;
DROP INDEX IF EXISTS scan_logs_client_op_uniq;
ALTER TABLE scan_logs DROP COLUMN IF EXISTS client_op_id, DROP COLUMN IF EXISTS device_id,
    DROP COLUMN IF EXISTS scanned_at_device, DROP COLUMN IF EXISTS source;
DROP TABLE IF EXISTS org_invitations;
