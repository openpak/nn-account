-- The console password cache: the 32 raw bytes a Wii U stores in account.dat
-- (AccountPasswordCache) and sends hex-encoded as the NNAS oauth20 password.
-- Hex-encoded here. Set at console registration from the transformed secret the
-- adapter already computes, or minted once by the emulator surface for
-- identities born there (NA-1a: the NNAS login Cemu performs needs a console
-- credential minted with the identity).
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS password_cache TEXT NOT NULL DEFAULT '';
