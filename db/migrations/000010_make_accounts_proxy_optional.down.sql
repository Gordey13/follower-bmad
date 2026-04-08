DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM accounts
        WHERE proxy_id IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot set accounts.proxy_id NOT NULL while NULL proxy bindings exist';
    END IF;
END $$;

ALTER TABLE accounts
    ALTER COLUMN proxy_id SET NOT NULL;
