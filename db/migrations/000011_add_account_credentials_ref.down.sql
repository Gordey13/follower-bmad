ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS chk_accounts_credential_ref_nonempty,
    DROP CONSTRAINT IF EXISTS chk_accounts_credential_source;

ALTER TABLE accounts
    DROP COLUMN IF EXISTS credential_ref,
    DROP COLUMN IF EXISTS credential_source;
