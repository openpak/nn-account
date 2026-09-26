-- The OS an emulator said it runs on ("windows", "macos", "linux", "android",
-- "ios"), from the X-OpenPak-Client header, next to the client it names.
-- nn-friends reads it back with the client and sends it with the core session.
-- Consoles, emulators that did not say, and tokens issued before this column
-- are ''.
ALTER TABLE nex_tokens ADD COLUMN IF NOT EXISTS os TEXT NOT NULL DEFAULT '';
