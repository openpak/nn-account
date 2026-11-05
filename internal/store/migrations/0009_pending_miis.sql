-- The Mii belongs to the account core (card #131). pnids no longer holds one: an upload the core could not take
-- yet waits in pending_miis (one row per PNID, retried every minute, deleted once the core has it) and the
-- column is gone. A Mii still in the column moves to the queue first (the "Default" placeholder is nobody's Mii).
CREATE TABLE IF NOT EXISTS pending_miis (
    pid        BIGINT PRIMARY KEY REFERENCES pnids(pid) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    attempts   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnids' AND column_name='mii_data') THEN
        INSERT INTO pending_miis (pid, data)
            SELECT pid, mii_data FROM pnids
            WHERE mii_data <> '' AND NOT deleted AND account_id IS NOT NULL
              AND mii_data <> 'AwAAQOlVognnx0GC2/uogAOzuI0n2QAAAEBEAGUAZgBhAHUAbAB0AAAAAAAAAEBAAAAhAQJoRBgmNEYUgRIXaA0AACkAUkhQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGm9'
            ON CONFLICT DO NOTHING;
        ALTER TABLE pnids DROP COLUMN mii_data;
    END IF;
END $$;
