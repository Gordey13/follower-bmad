ALTER TABLE accounts
    ADD COLUMN credential_source TEXT NOT NULL DEFAULT 'manual',
    ADD COLUMN credential_ref TEXT NOT NULL DEFAULT 'manual://legacy';

ALTER TABLE accounts
    ADD CONSTRAINT chk_accounts_credential_source CHECK (
        credential_source IN ('env', 'vault', 'file', 'manual')
    ),
    ADD CONSTRAINT chk_accounts_credential_ref_nonempty CHECK (
        NULLIF(BTRIM(credential_ref), '') IS NOT NULL
    );
