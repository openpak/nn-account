-- What a NEX token was issued to: "wiiu"/"3ds" for a console, or the emulator
-- named by X-OpenPak-Client ("cemu", "azahar"). nn-friends reads it back when
-- the token logs in and publishes it with the person's core session. Tokens
-- issued before this column are '' (shown as the platform).
ALTER TABLE nex_tokens ADD COLUMN IF NOT EXISTS client TEXT NOT NULL DEFAULT '';
