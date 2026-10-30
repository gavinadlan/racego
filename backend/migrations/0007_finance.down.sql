DROP TRIGGER IF EXISTS ledger_entries_no_truncate ON ledger_entries;
DROP TRIGGER IF EXISTS ledger_journals_no_truncate ON ledger_journals;
DROP TRIGGER IF EXISTS ledger_entries_immutable ON ledger_entries;
DROP TRIGGER IF EXISTS ledger_journals_immutable ON ledger_journals;
DROP FUNCTION IF EXISTS ledger_immutable();
DROP INDEX IF EXISTS ledger_journals_ref_idx;
DROP TABLE IF EXISTS refunds;
DROP INDEX IF EXISTS payouts_status_idx;
DROP INDEX IF EXISTS payouts_active_uniq;
DROP INDEX IF EXISTS payouts_idempotency_uniq;
ALTER TABLE payouts
    DROP CONSTRAINT IF EXISTS payouts_maker_checker,
    DROP COLUMN IF EXISTS idempotency_key, DROP COLUMN IF EXISTS account_snapshot, DROP COLUMN IF EXISTS reject_reason,
    DROP COLUMN IF EXISTS fail_reason, DROP COLUMN IF EXISTS attempts, DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS approved_at, DROP COLUMN IF EXISTS completed_at;
DELETE FROM payout_accounts WHERE NOT EXISTS (SELECT 1 FROM payouts p WHERE p.payout_account_id = payout_accounts.id);
ALTER TABLE payout_accounts
    DROP CONSTRAINT IF EXISTS payout_accounts_org_uniq,
    DROP COLUMN IF EXISTS verification_id, DROP COLUMN IF EXISTS account_last4, DROP COLUMN IF EXISTS account_enc,
    DROP COLUMN IF EXISTS account_holder, DROP COLUMN IF EXISTS bank_name,
    ADD COLUMN account_number text NOT NULL DEFAULT '',
    ADD COLUMN account_name text NOT NULL DEFAULT '';
