DROP TRIGGER IF EXISTS draw_winners_guard ON draw_winners;
DROP FUNCTION IF EXISTS draw_winners_guard();
DROP TABLE IF EXISTS draw_winners;
DROP TABLE IF EXISTS draw_prizes;
ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_attendance_window,
    DROP COLUMN IF EXISTS attendance_opens_at, DROP COLUMN IF EXISTS attendance_closes_at,
    DROP COLUMN IF EXISTS draw_eligibility, DROP COLUMN IF EXISTS allow_multiple_wins, DROP COLUMN IF EXISTS draw_public;
ALTER TABLE scan_logs DROP COLUMN IF EXISTS kind;
DROP INDEX IF EXISTS tickets_attended_idx;
ALTER TABLE tickets DROP COLUMN IF EXISTS attended_by, DROP COLUMN IF EXISTS attended_at;
